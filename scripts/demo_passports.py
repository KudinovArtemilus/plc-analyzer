"""Учебные паспорта за 30 дней — чтобы попробовать trend.py, пока настоящие не накопились.

Запуск:  python demo_passports.py D:\\plc-data\\demo
         python trend.py D:\\plc-data\\demo\\Пресс D:\\plc-data\\explore\\demo

Что заложено (trend.py должен это найти):
  - шаг 12 (ход оси) медленно растёт: 8 с → 9,6 с за месяц (+20 %) — износ, утечки;
  - масло к концу цикла греется сильнее: максимум 48 → 55 °C;
  - остальное — без тренда, только шум; одна смена пропущена (простой);
  - клапан последние 4 дня включается чаще — это скачок, а не тренд (его найдёт 34c).
"""
import os
import sys

import numpy as np
import pandas as pd

root = os.path.join(sys.argv[1] if len(sys.argv) > 1 else "demo", "Пресс", "passport")
rng = np.random.default_rng(1)  # одно и то же «случайное» — результат повторяется
t0 = pd.Timestamp.now().normalize() - pd.Timedelta(days=30) + pd.Timedelta(hours=8)
for d in range(30):
    for sh, h in (("A", 8), ("B", 20)):
        if d == 12 and sh == "B":
            continue  # смена простоя — паспорта нет
        start = t0 + pd.Timedelta(days=d, hours=h - 8)
        n, k = 320, d / 29
        s10 = rng.normal(2.0, 0.05, n)
        s12 = rng.normal(8.0 + 1.6 * k, 0.25, n)
        s12[rng.random(n) < 0.01] *= 3  # редкие зависания
        s14 = rng.normal(4.0, 0.08, n)
        cyc = s10 + s12 + s14 + rng.normal(0, 0.05, n)
        idle = np.abs(rng.normal(30, 10, n))
        times = start + pd.to_timedelta(np.cumsum(cyc + idle), unit="s")
        df = pd.DataFrame({
            "start": times.strftime("%Y-%m-%d %H:%M:%S.000"),
            "cycle_s": cyc.round(3), "idle_s": idle.round(3), "idle_cut": 0, "passes": 3,
            "slow_step": 12, "slow_s": s12.round(3),
            "step_10_s": s10.round(3), "step_12_s": s12.round(3), "step_14_s": s14.round(3),
            "Pressure.min": rng.normal(5, 0.5, n).round(1), "Pressure.max": rng.normal(180, 2, n).round(1),
            "Pressure.mean": rng.normal(90, 2, n).round(4),
            "OilTemp.min": rng.normal(40 + 4 * k, 0.5, n).round(1),
            "OilTemp.max": rng.normal(48 + 7 * k, 0.6, n).round(1),
            "OilTemp.mean": rng.normal(44 + 5 * k, 0.5, n).round(4),
            "Axis1.min": rng.normal(0.2, 0.05, n).round(2), "Axis1.max": rng.normal(850, 0.5, n).round(2),
            "Axis1.mean": rng.normal(420, 3, n).round(4),
            "Valve.on": rng.poisson(2 + (d > 25) * 0.6, n),
        })
        folder = os.path.join(root, f"{start:%Y}", f"{start:%m}", f"{start:%d}")
        os.makedirs(folder, exist_ok=True)
        df.to_csv(os.path.join(folder, f"{sh}_{start:%Y%m%d-%H%M%S}_g1.csv"), sep=";", decimal=",", index=False)
print(root)