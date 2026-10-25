// isa101.js — цвета графиков по ISA-101. Подключать ДО скриптов с графиками.
//
// Линии сигналов приглушённые и различимые, БЕЗ красного и жёлтого:
// эти цвета в проекте только для аварий и предупреждений.
const ISA = {
  traces: [
    "#1f4e79", // тёмно-синий
    "#6a3d9a", // фиолетовый
    "#00695c", // тёмная бирюза
    "#7a4a1e", // коричневый
    "#37474f", // графит
    "#3949ab", // индиго
    "#00838f", // морская волна
    "#5d4037", // тёмно-коричневый
  ],
  // Аварийные биты на графике: красный/жёлтый здесь ПО СМЫСЛУ — закрашено только там, где бит = 1,
  // то есть авария действительно была. Сама линия — нейтральная.
  alarmLine: "#5a5a5a",
  alarmFill: "rgba(176, 0, 0, 0.35)",   // как --alarm
  warnFill: "rgba(201, 154, 0, 0.45)", // как --warn
  cursorA: "#111111", // курсор A
  cursorB: "#0b5394", // курсор B
  axis: "#3a3a3a", // подписи осей
  grid: "#c2c2c2", // сетка
};

// isaAxes — оси для дорожки uPlot.
// Сетка по времени рисуется на ВСЕХ дорожках, а подписи времени — только на нижней.
// rightColor — цвет шкалы справа для наложенного сигнала; без него справа пустое место
// той же ширины: у всех дорожек одинаковая область графика, и время совпадает по вертикали.
function isaAxes(isLast, rightColor) {
  const common = {
    stroke: ISA.axis,
    grid: { stroke: ISA.grid, width: 1 },
    ticks: { stroke: ISA.grid, width: 1 },
  };
  const blank = (u, splits) => splits.map(() => "");
  const x = isLast
    ? { ...common }
    : { ...common, size: 4, values: blank }; // сетка есть, подписей нет
  const right = rightColor
    ? {
      scale: "y2", side: 1, size: 55, stroke: rightColor, grid: { show: false },
      ticks: { stroke: rightColor, width: 1 }
    }
    : { side: 1, size: 55, grid: { show: false }, ticks: { show: false }, values: blank };
  return [x, { ...common, size: 55 }, right];
}