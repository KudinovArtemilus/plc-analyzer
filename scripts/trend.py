"""Тренды по паспортам циклов: что в станке медленно меняется от смены к смене.

Запуск:  python trend.py <папка ПЛК> <папка для отчёта> [--days 60] [--warn 20]
         python trend.py "D:\\plc-data\\Пресс" D:\\plc-data\\explore\\trend

Берёт все паспорта ПЛК (<папка ПЛК>\\passport\\…\\*.csv, их пишет служба — 34a),
по каждому признаку считает «обычное» значение каждой смены (медиану) и смотрит,
куда оно движется: на сколько процентов изменилось, и — для времени шагов и цикла —
через сколько дней при таком темпе дойдёт до порога (по умолчанию +20 % от начала).

Код возврата 0 — отчёт готов (report.html); иначе текст ошибки в stderr.
"""
import argparse
import base64
import glob
import html
import io
import os
import sys

import numpy as np
import pandas as pd

import matplotlib
matplotlib.use("Agg")  # без окна — у службы его нет
import matplotlib.dates as mdates
import matplotlib.pyplot as plt

MIN_SHIFTS = 6   # меньше смен — о тренде говорить рано
CHARTS = 6       # графиков в отчёте — по самым заметно меняющимся признакам
SKIP = {"idle_cut", "passes", "slow_step", "slow_s"}  # не измерения; slow_s — каждый раз другой шаг


# ---------- Загрузка ----------

def shift_start(ts):
    """Начало смены для каждого момента: А — 08:00–20:00, Б — 20:00–08:00 (как в Go, archive.ShiftOf)."""
    day = ts.dt.normalize()
    h = ts.dt.hour
    start = day + pd.Timedelta(hours=8)                                    # смена А
    start = start.where(h < 20, day + pd.Timedelta(hours=20))              # вечер — Б этого дня
    start = start.where(h >= 8, day - pd.Timedelta(days=1) + pd.Timedelta(hours=20))  # ночь — Б вчерашнего
    return start


def load(plc_dir, days):
    """Все паспорта ПЛК за последние days дней — одна таблица, строка на цикл."""
    files = sorted(glob.glob(os.path.join(plc_dir, "passport", "**", "*.csv"), recursive=True))
    if not files:
        raise SystemExit(f"в {os.path.join(plc_dir, 'passport')} нет паспортов — настроен ли паспорт на странице «Шаги»?")
    base = os.path.join(plc_dir, "passport")
    parts = []
    for f in files:
        d = pd.read_csv(f, sep=";", decimal=",")
        if len(d):  # пустой паспорт (в записи не было сигнала шага) — только заголовок
            # Из какой записи цикл: 2026/10/28/A_….pla — так файл называет и «История» (для ссылок)
            rel = os.path.relpath(f, base).replace(os.sep, "/")
            parts.append(d.assign(file=rel[: -len(".csv")] + ".pla"))
    if not parts:
        raise SystemExit("паспорта есть, но циклов в них нет")
    # Столбцы у разных смен могут отличаться (добавили сигнал) — concat выровняет по именам, пропуски = NaN
    df = pd.concat(parts, ignore_index=True)
    df["start"] = pd.to_datetime(df["start"])
    df = df[df["start"] >= df["start"].max() - pd.Timedelta(days=days)]
    df["shift"] = shift_start(df["start"])
    return df.sort_values("start"), len(files)


# ---------- Расчёт ----------

def theil_sen(x, y):
    """Наклон прямой по Тейлу — Сену: медиана наклонов между всеми парами точек.
    Одна странная смена (наладка, полсмены простоя) прямую не перекосит — в отличие
    от обычного МНК, где один выброс тянет линию на себя."""
    i, j = np.triu_indices(len(x), k=1)
    slope = np.median((y[j] - y[i]) / (x[j] - x[i]))
    return slope, np.median(y - slope * x)


def is_duration(col):
    """Время цикла и шагов: рост — это плохо (ось медленнее, ожидание условия дольше)"""
    return col == "cycle_s" or (col.startswith("step_") and col.endswith("_s"))


def title(col):
    """Понятное имя признака"""
    if col == "cycle_s":
        return "Цикл, с"
    if col == "idle_s":
        return "Ожидание перед циклом, с"
    if col == "slow_s":
        return "Самый затянувшийся шаг цикла, с"
    if col.startswith("step_") and col.endswith("_s"):
        return f"Шаг {col[5:-2]}, с"
    name, _, kind = col.rpartition(".")
    return {"min": f"{name} — минимум за цикл", "max": f"{name} — максимум за цикл",
            "mean": f"{name} — среднее за цикл", "on": f"{name} — включений за цикл"}.get(kind, col)


def analyze(df, warn):
    """По каждому признаку: обычное значение в начале и сейчас, наклон, дней до порога"""
    t0 = df["shift"].min()
    out = []
    for col in df.columns:
        if col in ("start", "shift") or col in SKIP or not pd.api.types.is_numeric_dtype(df[col]):
            continue
        # «Обычное» значение смены — медиана. Кроме счётчиков включений: они целые (2, 2, 3, 2…),
        # и медиана застрянет на 2, даже когда в среднем стало 2,6 — для них берём среднее
        how = "mean" if col.endswith(".on") else "median"
        m = df.groupby("shift")[col].agg(how).dropna()
        if len(m) < MIN_SHIFTS or m.nunique() < 2:
            continue
        x = ((m.index - t0) / pd.Timedelta(days=1)).to_numpy()
        y = m.to_numpy(dtype=float)
        slope, b = theil_sen(x, y)

        third = max(len(m) // 3, 2)
        first, last = np.median(y[:third]), np.median(y[-third:])
        span = x[-1] - x[0]
        # Заметен ли тренд на фоне разброса смен: изменение по прямой за всё время
        # против обычного «дрожания» смен вокруг неё (межквартильный размах остатков)
        resid = y - (slope * x + b)
        noise = np.subtract(*np.percentile(resid, [75, 25]))
        clear = abs(slope * span) > 2 * max(noise, 1e-9)
        change = (last - first) / abs(first) * 100 if first != 0 else None

        row = dict(col=col, title=title(col), first=first, last=last, change=change,
                   week=slope * 7, clear=clear, shifts=len(m), m=m, fit=(slope, b, t0),
                   duration=is_duration(col), days_to=None, limit=None)
        if row["duration"] and first > 0:
            limit = first * (1 + warn / 100)
            now = slope * x[-1] + b
            row["limit"] = limit
            if now >= limit:
                row["days_to"] = 0.0
            elif slope > 0 and clear:
                row["days_to"] = (limit - now) / slope
        # Внимание: тренд заметный и изменение уже больше половины порога, или порог близко
        row["warn"] = clear and ((change is not None and abs(change) >= warn / 2) or
                                 (row["days_to"] is not None and row["days_to"] <= 30))
        out.append(row)
    # Сначала «внимание», потом заметные тренды, внутри — по величине изменения
    out.sort(key=lambda r: (not r["warn"], not r["clear"], -abs(r["change"] or 0)))
    return out


# ---------- Отчёт ----------

def fmt(v, digits=3):
    return "—" if v is None else f"{v:.{digits}g}".replace(".", ",")


def chart(df, r):
    """Точки — циклы, линия — обычное значение смены, пунктир — тренд, жёлтая — порог"""
    fig, ax = plt.subplots(figsize=(11, 2.8))
    col = df[["start", r["col"]]].dropna()
    lo, hi = col[r["col"]].quantile([0.01, 0.99])  # редкие зависания не сжимают шкалу
    pts = col[(col[r["col"]] >= lo) & (col[r["col"]] <= hi)]
    ax.scatter(pts["start"], pts[r["col"]], s=2, color="#A9B2B9", alpha=0.35, linewidths=0)
    ax.plot(r["m"].index, r["m"].values, color="#1F2A33", linewidth=1.4, marker="o", markersize=3)
    slope, b, t0 = r["fit"]
    xs = pd.DatetimeIndex([r["m"].index[0], r["m"].index[-1]])
    days = (xs - t0) / pd.Timedelta(days=1)
    ax.plot(xs, slope * days + b, color="#1F2A33", linestyle="--", linewidth=1)
    if r["limit"] is not None:
        ax.axhline(r["limit"], color="#D9A400", linewidth=1.5)
    ax.set_title(r["title"], loc="left", fontsize=10)
    ax.xaxis.set_major_formatter(mdates.DateFormatter("%d.%m"))  # 29.09, а не 2026-09-29 внахлёст
    ax.set_facecolor("#F2F1EC")
    ax.grid(color="#D5D3CC", linewidth=0.5)
    for side in ("top", "right"):
        ax.spines[side].set_visible(False)
    fig.tight_layout()
    buf = io.BytesIO()
    fig.savefig(buf, format="png", dpi=90)
    plt.close(fig)
    return base64.b64encode(buf.getvalue()).decode()


def verdict(r, warn):
    if r["days_to"] == 0:
        return f"<b>уже выше порога +{warn} %</b>"
    if r["days_to"] is not None:
        return f"порог +{warn} % — примерно через <b>{r['days_to']:.0f} дн.</b>"
    if not r["clear"]:
        return '<span class="muted">тренда не видно — колебания смен</span>'
    return "растёт" if r["week"] > 0 else "снижается"


def report(plc_dir, df, nfiles, rows, warn):
    name = os.path.basename(os.path.normpath(plc_dir))
    shifts = df["shift"].nunique()
    trs = []
    for r in rows:
        cls = ' class="warn-row"' if r["warn"] else ""
        ch = "—" if r["change"] is None else (
            "0,0 %" if abs(r["change"]) < 0.05 else f"{r['change']:+.1f} %".replace(".", ","))
        trs.append(f"<tr{cls}><td>{html.escape(r['title'])}</td><td class=num>{fmt(r['first'])}</td>"
                   f"<td class=num>{fmt(r['last'])}</td><td class=num>{ch}</td>"
                   f"<td class=num>{fmt(r['week'], 2)}</td><td>{verdict(r, warn)}</td></tr>")
    imgs = "".join(f'<img src="data:image/png;base64,{chart(df, r)}" alt="{html.escape(r["title"])}">'
                   for r in rows[:CHARTS] if r["clear"])
    note = ""
    if shifts < MIN_SHIFTS:
        note = (f'<div class="bar warn">Смен с циклами пока {shifts} — для тренда нужно хотя бы {MIN_SHIFTS}. '
                f'Паспорта копятся сами; отчёт станет содержательным через несколько дней.</div>')
    return f"""<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><title>Тренды — {html.escape(name)}</title>
<style>
body {{ font-family: Arial, sans-serif; background: #E6E4DE; color: #1F2A33; margin: 24px; }}
table {{ border-collapse: collapse; background: #F2F1EC; margin: 8px 0 16px; }}
th, td {{ border: 1px solid #C9C6BD; padding: 4px 10px; text-align: left; }}
td.num {{ text-align: right; font-family: Consolas, monospace; }}
tr.warn-row td {{ background: #F6E7A8; }}
.bar {{ padding: 8px 12px; background: #F2F1EC; border-left: 4px solid #8C99A3; margin: 8px 0; }}
.bar.warn {{ border-left-color: #D9A400; }}
.muted {{ color: #5A6B7A; }}
img {{ max-width: 100%; display: block; margin: 6px 0; }}
</style></head><body>
<h1>Тренды — {html.escape(name)}</h1>
<p>Циклов {len(df)}, смен {shifts} (файлов паспорта {nfiles}),
{df['start'].min():%d.%m.%Y} – {df['start'].max():%d.%m.%Y}. Порог для времени шагов и цикла — +{warn} % к началу периода.</p>
{note}
<p class="muted">«Обычно» — медиана смены: одно зависание её не сдвигает. «В начале» и «сейчас» — по первой
и последней трети смен. Наклон — по Тейлу — Сену (одна странная смена прямую не перекосит).
Жёлтым — тренд заметен на фоне разброса смен и изменение уже больше половины порога (или порог ближе 30 дней).</p>
<table><tr><th>Признак</th><th>В начале</th><th>Сейчас</th><th>Изменение</th><th>За неделю</th><th>Вывод</th></tr>
{"".join(trs)}
</table>
<h2>Самые заметные изменения</h2>
<p class="muted">Серые точки — циклы, линия — обычное значение смены, пунктир — тренд, жёлтая — порог.</p>
{imgs or '<p class="muted">Заметных трендов нет.</p>'}
</body></html>"""


def main():
    ap = argparse.ArgumentParser(description="Тренды по паспортам циклов PLC Analyzer")
    ap.add_argument("plc", help="папка ПЛК в D:\\plc-data (в ней папка passport)")
    ap.add_argument("out", help="папка, куда положить report.html")
    ap.add_argument("--days", type=int, default=60, help="за сколько последних дней (по умолчанию 60)")
    ap.add_argument("--warn", type=float, default=20, help="порог для времени шагов и цикла, %% (по умолчанию 20)")
    a = ap.parse_args()

    df, nfiles = load(a.plc, a.days)
    rows = analyze(df, a.warn)
    os.makedirs(a.out, exist_ok=True)
    dst = os.path.join(a.out, "report.html")
    with open(dst, "w", encoding="utf-8") as f:
        f.write(report(a.plc, df, nfiles, rows, a.warn))
    print(dst)
    return 0


if __name__ == "__main__":
    sys.exit(main())