"""Исследование смены: файл .pla → отчёт report.html.

Запуск:  python explore.py <файл.pla> <папка для отчёта>
Код возврата 0 — отчёт готов; иначе текст ошибки в stderr.
Скрипт только читает файл смены: ПЛК и службу он не трогает.
"""
import argparse
import base64
import html
import io
import os
import sys

import matplotlib
matplotlib.use("Agg")  # без окна: рисуем сразу в картинку (служба окон не имеет)
import matplotlib.pyplot as plt

from pla import read_pla

MAX_POINTS = 3000  # больше точек на графике глаз не различит, а картинка распухнет


def is_bit(s):
    """Дискретный сигнал: за смену принимал только значения 0 и 1."""
    return set(s.dropna().unique()) <= {0, 1}


def gaps(index, period_ms):
    """Разрывы записи: соседние точки дальше 3 периодов опроса (как в «Шагах»)."""
    d = index.to_series().diff().dt.total_seconds() * 1000
    g = d[d > 3 * period_ms]
    return len(g), g.sum() / 1000


def fmt(v):
    return f"{v:.6g}".replace(".", ",")  # 12,5 вместо 12.5 — как в остальном анализаторе


def signal_rows(df):
    """Строка таблицы на каждый сигнал."""
    rows = []
    for name in df.columns:
        s = df[name]
        if is_bit(s):
            ons = int(((s == 1) & (s.shift() == 0)).sum())  # фронты 0→1
            rows.append(f"<tr><td>{html.escape(name)}</td><td>бит</td>"
                        f"<td colspan=3>включался {ons} раз</td>"
                        f"<td>{s.mean() * 100:.1f} % времени в 1</td></tr>")
        else:
            rows.append(f"<tr><td>{html.escape(name)}</td><td>число</td>"
                        f"<td>{fmt(s.min())}</td><td>{fmt(s.max())}</td>"
                        f"<td>{fmt(s.mean())}</td><td>σ = {fmt(s.std())}</td></tr>")
    return "\n".join(rows)


def chart(df, units):
    """Все сигналы друг под другом, общая ось времени. Возвращает PNG в base64."""
    step = max(1, len(df) // MAX_POINTS)
    d = df.iloc[::step]  # прореживание: каждая step-я точка
    fig, axes = plt.subplots(len(df.columns), 1, sharex=True,
                             figsize=(12, 1.4 * len(df.columns) + 0.6), squeeze=False)
    for ax, name in zip(axes[:, 0], df.columns):
        s = d[name]
        if is_bit(df[name]):
            ax.fill_between(s.index, s.values, step="post", color="#5A6B7A", linewidth=0)
            ax.set_ylim(-0.1, 1.1)
            ax.set_yticks([0, 1])
        else:
            ax.plot(s.index, s.values, color="#1F2A33", linewidth=0.8)
        label = name + (f", {units[name]}" if units.get(name) else "")
        ax.set_ylabel(label, rotation=0, ha="right", va="center", fontsize=9)
        ax.set_facecolor("#F2F1EC")
        ax.grid(color="#D5D3CC", linewidth=0.5)
        for side in ("top", "right"):
            ax.spines[side].set_visible(False)
    fig.tight_layout()
    buf = io.BytesIO()
    fig.savefig(buf, format="png", dpi=90)
    plt.close(fig)
    return base64.b64encode(buf.getvalue()).decode()


def report(path, hdr, groups):
    parts = []
    for g, df in groups.items():
        info = hdr["groups"][g]
        period = info["period_ms"]
        units = {s["name"]: s.get("unit", "") for s in info["signals"]}
        n_gaps, gap_s = gaps(df.index, period)
        span = (df.index[-1] - df.index[0]).total_seconds()
        parts.append(f"""
<h2>Группа {g + 1}: опрос {period} мс, сигналов {len(df.columns)}</h2>
<p>Запись {df.index[0]:%H:%M:%S} – {df.index[-1]:%H:%M:%S}
({span / 60:.1f} мин), точек {len(df)}.
Разрывов записи: {n_gaps}{f" (всего {gap_s:.0f} с)" if n_gaps else ""}.</p>
<table><tr><th>Сигнал</th><th>Тип</th><th>min</th><th>max</th><th>среднее</th><th></th></tr>
{signal_rows(df)}
</table>
<img src="data:image/png;base64,{chart(df, units)}" alt="графики группы {g + 1}">""")

    title = f"{hdr['plc']} — смена {hdr['shift']}, {hdr['shift_date']}"
    return f"""<!doctype html>
<html lang="ru"><head><meta charset="utf-8"><title>{html.escape(title)}</title>
<style>
body {{ font-family: Arial, sans-serif; background: #E6E4DE; color: #1F2A33; margin: 24px; }}
table {{ border-collapse: collapse; background: #F2F1EC; margin: 8px 0 16px; }}
th, td {{ border: 1px solid #C9C6BD; padding: 4px 10px; text-align: left; }}
img {{ max-width: 100%; background: #F2F1EC; }}
.note {{ color: #5A6B7A; font-size: 90%; }}
</style></head><body>
<h1>{html.escape(title)}</h1>
<p class="note">Файл {html.escape(os.path.basename(path))} · Python-исследование (33a)</p>
{"".join(parts)}
</body></html>"""


def main():
    ap = argparse.ArgumentParser(description="Исследование смены PLC Analyzer")
    ap.add_argument("pla", help="файл смены .pla")
    ap.add_argument("out", help="папка, куда положить report.html")
    a = ap.parse_args()

    hdr, groups = read_pla(a.pla)
    if not groups:
        print("в файле нет данных", file=sys.stderr)
        return 2
    os.makedirs(a.out, exist_ok=True)
    dst = os.path.join(a.out, "report.html")
    with open(dst, "w", encoding="utf-8") as f:
        f.write(report(a.pla, hdr, groups))
    print(dst)  # Go прочитает путь из stdout (пригодится в 33b)
    return 0


if __name__ == "__main__":
    sys.exit(main())