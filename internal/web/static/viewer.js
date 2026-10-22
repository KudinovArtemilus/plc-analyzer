// viewer.js — дорожки сигналов: общая ось времени, курсоры A/B, Δt, синхронный зум.
// Сейчас его использует страница «История»; позже на него переедет и Live.

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
function createViewer(cfg) {
  const LANE_H = 80, NAME_W = 130, VAL_W = 110;
  const COLOR_A = ISA.cursorA, COLOR_B = ISA.cursorB;
  const colors = ISA.traces;
  const plotWidth = () => window.innerWidth - NAME_W - VAL_W - 40;
  const sync = uPlot.sync("viewer");
  const changed = () => { if (cfg.onChange) cfg.onChange(); };

  let lanes = [];
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
      ctx.strokeStyle = color;
      ctx.lineWidth = 2 * window.devicePixelRatio;
      ctx.beginPath();
      ctx.moveTo(x, top);
      ctx.lineTo(x, top + height);
      ctx.stroke();
      ctx.restore();
    });
  }

  function valueAt(u, t) {
    const xs = u.data[0], ys = u.data[1];
    const i = nearestIdx(xs, t);
    if (i < 0) return "—";
    for (const k of [i, i - 1, i + 1]) {
      if (k >= 0 && k < ys.length && ys[k] != null) return fmtVal(ys[k]);
    }
    return "—";
  }

  function showValues(l) {
    const parts = [];
    if (curA != null) parts.push("A " + valueAt(l.u, curA));
    if (curB != null) parts.push("B " + valueAt(l.u, curB));
    const idx = l.u.cursor.idx;
    if (idx != null && l.u.data[1][idx] != null) parts.push("• " + fmtVal(l.u.data[1][idx]));
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
    lanes.forEach(l => l.u.setScale("x", { min, max }));
    changed();
  }

  function build(defs) {
    cfg.lanesEl.textContent = "";
    lanes = defs.map((d, i) => {
      const row    = document.createElement("div");
      const nameEl = document.createElement("div");
      const plotEl = document.createElement("div");
      const valEl  = document.createElement("div");
      row.className = "lane";
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

      const isLast = i === defs.length - 1;
      const u = new uPlot({
        width:  plotWidth(),
        height: LANE_H + (isLast ? 30 : 0),
        legend: { show: false },
        cursor: {
          sync: { key: sync.key },
          drag: { x: true, y: false, setScale: false },
        },
        hooks: {
          draw: [drawCursors],
          setCursor: [u => {
            const l = lanes[i];
            if (l) showValues(l);
            if (i === 0 && cfg.hoverEl) {
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
        axes: isaAxes(isLast),
        series: [{}, { stroke: colors[i % colors.length], width: 1 }],
      }, [[], []], plotEl);

      u.over.addEventListener("click", e => {
        if (justZoomed) { justZoomed = false; return; }
        setCursor("A", u, e.offsetX);
      });
      u.over.addEventListener("contextmenu", e => {
        e.preventDefault();
        setCursor("B", u, e.offsetX);
      });

      return { u, valEl, name: d.name, sub: subText };
    });
  }

  window.addEventListener("resize", () => {
    lanes.forEach((l, i) => l.u.setSize({
      width: plotWidth(),
      height: LANE_H + (i === lanes.length - 1 ? 30 : 0),
    }));
  });
  document.addEventListener("keydown", e => { if (e.key === "Escape") clearCursors(); });
  if (cfg.clearBtn) cfg.clearBtn.onclick = clearCursors;

  return {
    build,
    setRange,
    setSeries: (i, xs, ys) => lanes[i].u.setData([xs, ys], false),
    updateCursors,
    clearCursors,
    getLanes: () => lanes,
    getCursors: () => ({ a: curA, b: curB }),
    getRange: () => lanes.length
      ? { min: lanes[0].u.scales.x.min, max: lanes[0].u.scales.x.max }
      : null,
    cursorsText,
  };
}
