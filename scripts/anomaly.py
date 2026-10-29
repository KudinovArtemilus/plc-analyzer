"""Аномалии по паспортам циклов: какие циклы смены непохожи на обычные — и чем.

Запуск:  python anomaly.py <папка ПЛК> <папка для отчёта> [--plc 1 --step Шаги --mode Режим]
         python anomaly.py D:\\plc-data\\SGGT D:\\plc-data\\explore\\anomaly --plc 1 --step "Шаги" --mode "Режим"

Тренд (trend.py) ловит медленный износ за недели. Здесь — внезапное:
  1) отдельные циклы, где какой-то признак далеко от обычного (шаг завис, давление
     не добрало, клапан щёлкнул лишний раз) — с объяснением и ссылкой на график;
  2) смена в целом, если какой-то признак у неё сдвинут относительно прошлых смен.

«Обычное» берётся из прошлых смен (последней смены в нём нет — иначе она сама себя оправдала бы).
Мера «насколько необычно» — робастная z-оценка: (значение − медиана) / разброс, где разброс —
медиана абсолютных отклонений (MAD). Среднее и σ сами портятся выбросами, которые мы ищем;
медиана и MAD — нет.

--plc — номер ПЛК в анализаторе (из адреса страниц: ?plc=1): с ним у циклов будет «график ↗»;
--step, --mode — имена сигналов шага и режима (как на странице «Шаги»): их тоже покажет график.
"""
import argparse
from datetime import datetime
import html
import os
import sys
from urllib.parse import urlencode

import pandas as pd

from trend import SKIP, load, title  # загрузка и имена признаков — общие с trend.py

Z_CYCLE = 5    # цикл необычный, если хоть один признак дальше 5 «разбросов» от обычного
Z_SHIFT = 4    # смена сдвинута, если её обычное значение дальше 4 разбросов от прошлых смен
LOCAL = datetime.now().astimezone().tzinfo  # часовой пояс Windows — как у службы
TOP = 20       # строк в таблице необычных циклов
MIN_BASE = 50  # меньше циклов в «норме» — сравнивать смену с самой собой


def features(df):
    return [c for c in df.columns if c not in ("start", "shift", "file") and c not in SKIP
            and pd.api.types.is_numeric_dtype(df[c])]


def scale(s, col):
    """Разброс признака: 1,4826 × MAD (для нормального распределения это σ).
    Снизу ограничен — у почти постоянного сигнала MAD бывает 0, и любая мелочь стала бы «аномалией»:
      - размах 10–90 % значений / 2,563 (тоже σ для нормального) — если больше половины циклов
        одинаковые (упор, насыщение), а остальные нет, MAD = 0, а этот размах — нет;
      - 2 % от обычного значения; у счётчиков включений — 1 (одно включение)."""
    med = s.median()
    mad = (s - med).abs().median() * 1.4826
    p10, p90 = s.quantile([0.1, 0.9])
    floor = 1.0 if col.endswith(".on") else 0.02 * abs(med)
    return med, max(mad, (p90 - p10) / 2.563, floor, 1e-9)


def unusual_cycles(df, last, cols, zmax):
    """Циклы последней смены: робастные z по всем признакам, самые необычные сверху"""
    base = df[df["shift"] != last]
    if len(base) < MIN_BASE:
        base = df  # истории нет — сравниваем смену с самой собой: выбросы внутри смены видно всё равно
    cur = df[df["shift"] == last]
    stats = {c: scale(base[c].dropna(), c) for c in cols if base[c].notna().sum() >= 10}

    z = pd.DataFrame({c: (cur[c] - m) / s for c, (m, s) in stats.items()}, index=cur.index)
    score = z.abs().max(axis=1)
    out = []
    for i in score[score >= zmax].sort_values(ascending=False).index:
        why = z.loc[i].abs().sort_values(ascending=False)
        why = why[why >= zmax][:3]  # до трёх признаков — чем именно необычен
        out.append(dict(row=cur.loc[i], score=score[i], why=[
            (c, cur.at[i, c], stats[c][0], z.at[i, c]) for c in why.index]))
    return out, len(cur), len(base) if base is not df else 0


def shifted(df, last, cols, zmax):
    """Признаки, по которым смена в целом отличается от прошлых: её обычное значение
    против обычных значений прошлых смен. Нужны хотя бы 3 прошлые смены."""
    per = {}
    for c in cols:
        how = "mean" if c.endswith(".on") else "median"  # как в trend.py
        per[c] = df.groupby("shift")[c].agg(how)
    per = pd.DataFrame(per)
    prev = per.drop(index=last, errors="ignore").dropna(how="all")
    if len(prev) < 3 or last not in per.index:
        return None
    out = []
    for c in cols:
        p = prev[c].dropna()
        if len(p) < 3 or pd.isna(per.at[last, c]):
            continue
        med = p.median()
        # Разброс МЕЖДУ сменами — меньше, чем между циклами: смена усредняет сотни циклов
        mad = max((p - med).abs().median() * 1.4826, 0.01 * abs(med), 1e-9)
        zz = (per.at[last, c] - med) / mad
        if abs(zz) >= zmax:
            out.append((c, per.at[last, c], med, zz))
    out.sort(key=lambda r: -abs(r[3]))
    return out


# ---------- Отчёт ----------

def fmt(v):
    return f"{v:.4g}".replace(".", ",")


def link(r, args, signals):
    """Цикл на графике анализатора: участок с запасом, курсоры A и B на начале и конце цикла"""
    if args.plc is None:
        return ""
    # Время в паспорте — местное (как его пишет служба), а адрес ждёт миллисекунды Unix
    t0 = int(r["row"]["start"].tz_localize(LOCAL).timestamp() * 1000)
    t1 = t0 + int(r["row"]["cycle_s"] * 1000)
    pad = max(5000, (t1 - t0) // 10)
    # На графике: шаг и режим (если указаны) и сигналы, которыми цикл необычен.
    # У «Шаг 12» и «Цикл» своего сигнала нет — их видно по сигналу шага (--step).
    names = [n for n in (args.step, args.mode) if n]
    names += [c.rpartition(".")[0] for c, *_ in r["why"] if "." in c]
    if not names:
        names = signals[:8]  # не знаем, что показать, — первые сигналы записи
    q = [("plc", args.plc), ("file", r["row"]["file"])]
    q += [("s", n) for n in dict.fromkeys(names)]  # dict.fromkeys — убрать повторы, сохранив порядок
    q += [("from", t0 - pad), ("to", t1 + pad), ("ca", t0), ("cb", t1)]
    return f'<a href="{html.escape(args.url.rstrip("/") + "/view.html?" + urlencode(q))}" target="_blank">график ↗</a>'


def report(plc_dir, df, last, cyc, ncur, nbase, sh, args):
    name = os.path.basename(os.path.normpath(plc_dir))
    shift_name = ("А" if last.hour == 8 else "Б") + f", {last:%d.%m.%Y}"
    signals = list(dict.fromkeys(c.rpartition(".")[0] for c in features(df) if "." in c))
    rows = []
    for r in cyc[:args.top]:
        why = "; ".join(f"{html.escape(title(c))}: <b>{fmt(v)}</b> (обычно {fmt(m)})" for c, v, m, z in r["why"])
        rows.append(f"<tr><td class=mono>{r['row']['start']:%H:%M:%S}</td><td class=num>{fmt(r['row']['cycle_s'])}</td>"
                    f"<td class=num>{r['score']:.0f}</td><td>{why}</td><td>{link(r, args, signals)}</td></tr>")
    base_note = (f"с {nbase} циклами прошлых смен" if nbase else
                 "с остальными циклами этой же смены (прошлых смен пока мало)")
    if cyc:
        many = ""
        if len(cyc) > 0.1 * ncur:  # каждый десятый «необычный» — скорее, необычна сама норма
            many = ('<p class="bar">Необычных больше 10 % — скорее всего, «обычное» ещё не устоялось '
                    '(мало циклов, наладка) или смена вся идёт иначе — смотрите «Смена в целом».</p>')
        cyc_html = (f"<p>Необычных циклов: <b>{len(cyc)}</b> из {ncur} ({len(cyc) / ncur * 100:.1f} %). "
                    f"Показаны {min(len(cyc), args.top)} самых необычных.</p>{many}"
                    "<table><tr><th>Начало</th><th>Цикл, с</th><th>Насколько</th><th>Чем необычен</th><th></th></tr>"
                    + "".join(rows) + "</table>")
    else:
        cyc_html = f"<p>Из {ncur} циклов необычных нет — все в пределах {args.z} разбросов от обычного.</p>"

    if sh is None:
        sh_html = '<p class="muted">Нужны хотя бы 3 прошлые смены с циклами — сравнивать смену пока не с чем.</p>'
    elif not sh:
        sh_html = "<p>Смена в целом не отличается от прошлых.</p>"
    else:
        sh_html = ("<table><tr><th>Признак</th><th>В этой смене</th><th>Обычно в прошлых</th><th>Насколько</th></tr>"
                   + "".join(f'<tr class="warn-row"><td>{html.escape(title(c))}</td><td class=num>{fmt(v)}</td>'
                             f"<td class=num>{fmt(m)}</td><td class=num>{z:+.0f}</td></tr>" for c, v, m, z in sh)
                   + "</table>")

    return f"""<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><title>Аномалии — {html.escape(name)}</title>
<style>
body {{ font-family: Arial, sans-serif; background: #E6E4DE; color: #1F2A33; margin: 24px; }}
table {{ border-collapse: collapse; background: #F2F1EC; margin: 8px 0 16px; }}
th, td {{ border: 1px solid #C9C6BD; padding: 4px 10px; text-align: left; vertical-align: top; }}
td.num, td.mono {{ text-align: right; font-family: Consolas, monospace; }}
tr.warn-row td {{ background: #F6E7A8; }}
.bar {{ padding: 8px 12px; background: #F2F1EC; border-left: 4px solid #D9A400; }}
.muted {{ color: #5A6B7A; }}
</style></head><body>
<h1>Аномалии — {html.escape(name)}, смена {shift_name}</h1>
<p class="muted">Циклы смены сравниваются {base_note}. «Насколько» — во сколько раз отклонение больше
обычного разброса признака (робастная z-оценка: медиана и MAD, их выбросы не портят). Необычный цикл —
хотя бы один признак дальше {args.z} разбросов.</p>
<h2>Необычные циклы</h2>
{cyc_html}
<h2>Смена в целом</h2>
<p class="muted">Обычное значение признака в этой смене против прошлых смен — ловит скачок
(например, клапан с какого-то дня включается чаще), который тренд за месяц ещё не видит.</p>
{sh_html}
</body></html>"""


def main():
    ap = argparse.ArgumentParser(description="Аномалии по паспортам циклов PLC Analyzer")
    ap.add_argument("plc_dir", help="папка ПЛК в D:\\plc-data (в ней папка passport)")
    ap.add_argument("out", help="папка, куда положить report.html")
    ap.add_argument("--plc", type=int, help="номер ПЛК в анализаторе (?plc=…) — для ссылок «график ↗»")
    ap.add_argument("--step", help="сигнал номера шага — показать его на графике цикла")
    ap.add_argument("--mode", help="сигнал режима работы — тоже на график")
    ap.add_argument("--url", default="http://127.0.0.1:8080", help="адрес анализатора для ссылок")
    ap.add_argument("--days", type=int, default=30, help="за сколько дней брать «обычное» (по умолчанию 30)")
    ap.add_argument("--z", type=float, default=Z_CYCLE, help=f"порог необычности цикла (по умолчанию {Z_CYCLE})")
    ap.add_argument("--top", type=int, default=TOP, help=f"строк в таблице (по умолчанию {TOP})")
    args = ap.parse_args()

    df, _ = load(args.plc_dir, args.days)
    last = df["shift"].max()
    cols = features(df)
    cyc, ncur, nbase = unusual_cycles(df, last, cols, args.z)
    sh = shifted(df, last, cols, Z_SHIFT)

    os.makedirs(args.out, exist_ok=True)
    dst = os.path.join(args.out, "report.html")
    with open(dst, "w", encoding="utf-8") as f:
        f.write(report(args.plc_dir, df, last, cyc, ncur, nbase, sh, args))
    print(dst)
    return 0


if __name__ == "__main__":
    sys.exit(main())