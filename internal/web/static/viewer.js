// viewer.js — дорожки сигналов: общая ось времени, курсоры A/B, Δt, синхронный зум,
// перестановка дорожек, наложение второго сигнала. Сейчас его использует страница «История».
// Подключать ПОСЛЕ isa101.js, uPlot и lanes.js.

function nearestIdx(xd, t) {
  if (xd.length === 0) return -1;
  let lo = 0, hi = xd.length - 1;
  while (lo < hi) {
    const mid = (lo + hi) >> 1;
    if (xd[mid] < t) lo = mid + 1; else hi = mid;
  }
  if (lo > 0 && t - xd[lo - 1] < xd[lo] - t) lo--;
  return lo;
}

function fmtTime(sec) {
  const d = new Date(sec * 1000);
  return d.toLocaleTimeString("ru-RU", { hour12: false }) + "." +
    String(d.getMilliseconds()).padStart(3, "0");
}

function fmtPeriod(ms) {
  return ms >= 1000 ? (ms / 1000) + " с" : ms + " мс";
}

function fmtVal(v) {
  return Number.isInteger(v) ? String(v) : String(+v.toFixed(3));
}

// createViewer(cfg)
//   cfg.lanesEl, cfg.hoverEl, cfg.cursorsEl, cfg.cursorTextEl, cfg.clearBtn — элементы страницы
//   cfg.onZoom(min, max) — выделили участок (секунды)
//   cfg.onChange()       — сменились курсоры или видимый участок
//   cfg.orderKey         — под каким именем запоминать порядок дорожек и наложения (номер ПЛК)
function createViewer(cfg) {
  const LANE_H = 80, NAME_W = 130, VAL_W = 110;
  const COLOR_A = ISA.cursorA, COLOR_B = ISA.cursorB;
  const colors = ISA.traces;
  const plotWidth = () => window.innerWidth - NAME_W - VAL_W - 40;
  const sync = uPlot.sync("viewer");
  const changed = () => { if (cfg.onChange) cfg.onChange(); };

  let lanes = [];
  let axisU = null;    // строка с осью времени под дорожками
  let order = null;    // перестановка дорожек
  let overlays = null; // наложения
  let range = null;    // видимый участок { min, max }
  let curA = null, curB = null;
  let justZoomed = false;

  function drawCursors(u) {
    const ctx = u.ctx;
    const { left, top, width, height } = u.bbox;
    [[curA, COLOR_A], [curB, COLOR_B]].forEach(([t, color]) => {
      if (t == null) return;
      const x = Math.round(u.valToPos(t, "x", true));
      if (x < left || x > left + width) return;
      ctx.save();
      ctx.setLineDash([]); // пунктир наложенной линии не должен достаться курсору
      ctx.strokeStyle = color;
      ctx.lineWidth = 2 * window.devicePixelRatio;
      ctx.beginPath();
      ctx.moveTo(x, top);
      ctx.lineTo(x, top + height);
      ctx.stroke();
      ctx.restore();
    });
  }

  // Наложенная на дорожку l дорожка (или null)
  function ovLane(l) {
    const o = overlays && overlays.get(l.name);
    return o ? lanes.find(x => x.name === o.name) || null : null;
  }

  // Значение сигнала в момент t — по его СОБСТВЕННЫМ точкам, а не по общему набору графика
  // (в общем наборе после слияния у сигнала бывают null)
  function valueAt(src, t) {
    if (!src) return "—";
    const i = nearestIdx(src[0], t);
    return i < 0 || src[1][i] == null ? "—" : fmtVal(src[1][i]);
  }

  // «12» или «12 / 7.5», если на дорожку наложен второй сигнал
  function valuesAt(l, t) {
    const o = ovLane(l);
    return valueAt(l.src, t) + (o ? " / " + valueAt(o.src, t) : "");
  }

  function showValues(l) {
    const parts = [];
    if (curA != null) parts.push("A " + valuesAt(l, curA));
    if (curB != null) parts.push("B " + valuesAt(l, curB));
    const idx = l.u.cursor.idx;
    if (idx != null && l.u.data[0][idx] != null) parts.push("• " + valuesAt(l, l.u.data[0][idx]));
    l.valEl.textContent = parts.join("\n");
  }

  // Текст про курсоры: «A: … B: … Δt = …»
  function cursorsText() {
    const parts = [];
    if (curA != null) parts.push("A: " + fmtTime(curA));
    if (curB != null) parts.push("B: " + fmtTime(curB));
    if (curA != null && curB != null) parts.push("Δt = " + (curB - curA).toFixed(3) + " с");
    return parts.join("    ");
  }

  function updateCursors() {
    lanes.forEach(l => { l.u.redraw(false); showValues(l); });

    if (curA == null && curB == null) {
      cfg.cursorsEl.hidden = true;
    } else {
      cfg.cursorsEl.hidden = false;
      const el = cfg.cursorTextEl;
      el.textContent = "";
      const add = (cls, text) => {
        const s = document.createElement("span");
        if (cls) s.className = cls;
        s.textContent = text;
        el.append(s, "    ");
      };
      if (curA != null) add("a", "A: " + fmtTime(curA));
      if (curB != null) add("b", "B: " + fmtTime(curB));
      if (curA != null && curB != null) add("", "Δt = " + (curB - curA).toFixed(3) + " с");
    }
    changed();
  }

  function setCursor(which, u, px) {
    const i = nearestIdx(u.data[0], u.posToVal(px, "x"));
    if (i < 0) return;
    const t = u.data[0][i];
    if (which === "A") curA = t; else curB = t;
    updateCursors();
  }

  function clearCursors() {
    curA = curB = null;
    updateCursors();
  }

  function setRange(min, max) {
    range = { min, max };
    lanes.forEach(l => l.u.setScale("x", { min, max }));
    if (axisU) axisU.setScale("x", { min, max });
    changed();
  }

  // Данные для графика дорожки: свой ряд или, при наложении, оба ряда на общей оси времени
  function laneData(l) {
    if (!l.src) return [[], []];
    const o = ovLane(l);
    if (!o) return l.src;
    if (!o.src) return [l.src[0], l.src[1], l.src[1].map(() => null)];
    return mergeXY(l.src[0], l.src[1], o.src[0], o.src[1]);
  }

  // makePlot — (пере)создать график дорожки. Вызывается при построении и когда
  // на дорожку наложили сигнал или убрали его: число линий и шкал у uPlot задаётся
  // при создании, поэтому проще создать график заново (данные хранятся в l.src).
  function makePlot(l) {
    if (l.u) l.u.destroy();
    const o = ovLane(l);
    const ov = o ? { color: overlayColor(l.idx, o.idx), shared: overlays.get(l.name).shared } : null;
    l.ovName = o ? o.name : "";
    l.ovColor = ov ? ov.color : "";

    const u = new uPlot({
      width: plotWidth(),
      height: LANE_H,
      legend: { show: false },
      cursor: {
        sync: { key: sync.key },
        drag: { x: true, y: false, setScale: false },
      },
      hooks: {
        draw: [drawCursors],
        setCursor: [u => {
          showValues(l);
          if (cfg.hoverEl && !l.row.hidden) {
            const idx = u.cursor.idx;
            cfg.hoverEl.textContent = idx != null
              ? "курсор мыши: " + fmtTime(u.data[0][idx]) : "";
          }
        }],
        setSelect: [u => {
          if (u.select.width < 5) return;
          justZoomed = true;
          const min = u.posToVal(u.select.left, "x");
          const max = u.posToVal(u.select.left + u.select.width, "x");
          u.setSelect({ left: 0, top: 0, width: 0, height: 0 }, false);
          setRange(min, max);
          if (cfg.onZoom) cfg.onZoom(min, max);
        }],
      },
      // подписи времени — в отдельной строке под дорожками; справа — шкала наложенного сигнала
      axes: isaAxes(false, ov && !ov.shared ? ov.color : null),
      series: laneSeries(colors[l.idx % colors.length], ov),
    }, laneData(l), l.plotEl);

    u.over.addEventListener("click", e => {
      if (justZoomed) { justZoomed = false; return; }
      setCursor("A", u, e.offsetX);
    });
    u.over.addEventListener("contextmenu", e => {
      e.preventDefault();
      setCursor("B", u, e.offsetX);
    });

    l.u = u;
    const line = l.row.querySelector(".name .ovl"); // подпись «+ имя» — цветом пунктира
    if (line && ov) line.style.color = ov.color;
    if (range) u.setScale("x", range);
    showValues(l);
  }

  function build(defs) {
    cfg.lanesEl.textContent = "";
    lanes.forEach(l => l.u && l.u.destroy());
    if (axisU) { // пересборка: убираем старую ось
      axisU.root.parentElement.remove();
      axisU.destroy();
      axisU = null;
    }
    lanes = defs.map((d, i) => {
      const row = document.createElement("div");
      const nameEl = document.createElement("div");
      const plotEl = document.createElement("div");
      const valEl = document.createElement("div");
      row.className = "lane";
      row.dataset.name = d.name; // по имени запоминаются порядок дорожек и наложения
      nameEl.className = "name";
      valEl.className = "val";

      const subText = (d.unit ? d.unit + " · " : "") + fmtPeriod(d.periodMs);
      const nm = document.createElement("div");
      nm.textContent = d.name;
      const sub = document.createElement("div");
      sub.className = "period";
      sub.textContent = subText;
      nameEl.append(nm, sub);
      nameEl.title = d.name;

      row.append(nameEl, plotEl, valEl);
      cfg.lanesEl.appendChild(row);

      return { idx: i, u: null, row, plotEl, valEl, name: d.name, sub: subText, src: null, ovName: "", ovColor: "" };
    });

    if (!lanes.length) return;
    lanes.forEach(makePlot);
    axisU = makeTimeAxis(cfg.lanesEl, plotWidth());
    order = makeSortable(cfg.lanesEl, cfg.orderKey || "default");
    overlays = makeOverlays(cfg.lanesEl, cfg.orderKey || "default", name => {
      const l = lanes.find(x => x.name === name);
      if (l) makePlot(l);
      changed();
    });
    // Запомненные наложения: теперь overlays есть — перерисовать такие дорожки с двумя линиями
    lanes.filter(l => overlays.get(l.name)).forEach(makePlot);
  }

  // setSeries — пришли точки сигнала номер i: обновить его дорожку
  // и дорожку, на которую он наложен
  function setSeries(i, xs, ys) {
    lanes[i].src = [xs, ys];
    lanes.forEach(l => {
      const o = ovLane(l);
      if (l.idx === i || (o && o.idx === i)) l.u.setData(laneData(l), false);
    });
  }

  // Видимые дорожки в том порядке, в каком они сейчас на экране
  // (наложенные на другие дорожки — спрятаны, их не отдаём)
  function visualLanes() {
    return [...cfg.lanesEl.children]
      .filter(r => !r.hidden)
      .map(r => lanes.find(l => l.row === r))
      .filter(Boolean);
  }

  window.addEventListener("resize", () => {
    lanes.forEach(l => l.u.setSize({ width: plotWidth(), height: LANE_H }));
    if (axisU) axisU.setSize({ width: plotWidth(), height: 34 });
  });
  document.addEventListener("keydown", e => { if (e.key === "Escape") clearCursors(); });
  if (cfg.clearBtn) cfg.clearBtn.onclick = clearCursors;

  return {
    build,
    setRange,
    setSeries,
    updateCursors,
    clearCursors,
    getLanes: visualLanes,
    getAxis: () => axisU,
    resetOrder: () => { if (order) order.reset(); },
    getCursors: () => ({ a: curA, b: curB }),
    getRange: () => range,
    cursorsText,
  };
}