// lanes.js — общее для дорожек на всех страницах:
//   makeSortable — перестановка дорожек (перетаскивание за ⠿ и кнопки ▲▼), порядок запоминается;
//   makeTimeAxis — отдельная строка с осью времени, всегда под дорожками;
//   makeOverlays — наложение второго сигнала на дорожку (кнопка ⊕), запоминается;
//   mergeXY      — два ряда с разным шагом времени в один набор для uPlot.
// Подключать ПОСЛЕ isa101.js и uPlot.

// makeSortable(container, key)
//   container — элемент со строками .lane, у каждой строки data-name = имя сигнала
//   key       — под каким именем запомнить порядок (например, номер ПЛК)
// Возвращает { reset() } — вернуть исходный порядок.
function makeSortable(container, key) {
    const storageKey = "plca:order:" + key;
    const rows = () => [...container.querySelectorAll(":scope > .lane")];
    const defaultOrder = rows().map(r => r.dataset.name);
    let dragged = null;

    // После переноса строк uPlot должен заново узнать, где на экране его холст
    const changed = () => window.dispatchEvent(new Event("resize"));

    function save() {
        try {
            localStorage.setItem(storageKey, JSON.stringify(rows().map(r => r.dataset.name)));
        } catch (e) { /* браузер запретил хранилище — просто не запоминаем */ }
    }

    // apply — расставить строки по списку имён; сигналы, которых нет в списке, — в конец
    function apply(order) {
        const byName = new Map(rows().map(r => [r.dataset.name, r]));
        const placed = new Set();
        for (const n of [...order, ...defaultOrder]) {
            const r = byName.get(n);
            if (r && !placed.has(n)) {
                container.appendChild(r); // appendChild ПЕРЕНОСИТ строку, а не копирует её
                placed.add(n);
            }
        }
    }

    // Соседняя ВИДИМАЯ строка: скрытые (наложенные на другую дорожку) пропускаем
    function sibling(row, dir) {
        let r = dir < 0 ? row.previousElementSibling : row.nextElementSibling;
        while (r && r.hidden) r = dir < 0 ? r.previousElementSibling : r.nextElementSibling;
        return r;
    }

    function move(row, dir) {
        const other = sibling(row, dir);
        if (!other) return;
        container.insertBefore(row, dir < 0 ? other : other.nextElementSibling);
        save();
        changed();
    }

    // Ручка ⠿ и кнопки ▲▼ в колонке имени каждой дорожки
    rows().forEach(row => {
        const ord = document.createElement("span");
        ord.className = "ord";

        const grip = document.createElement("span");
        grip.className = "grip";
        grip.textContent = "⠿";
        grip.title = "перетащить дорожку";
        grip.draggable = true;

        const up = document.createElement("button");
        up.textContent = "▲";
        up.title = "выше";
        up.onclick = () => move(row, -1);

        const down = document.createElement("button");
        down.textContent = "▼";
        down.title = "ниже";
        down.onclick = () => move(row, +1);

        ord.append(grip, up, down);
        row.querySelector(".name").prepend(ord);

        grip.addEventListener("dragstart", e => {
            dragged = row;
            row.classList.add("dragging");
            e.dataTransfer.effectAllowed = "move";
            e.dataTransfer.setData("text/plain", row.dataset.name); // без этого Firefox не начнёт перетаскивание
            e.dataTransfer.setDragImage(row, 20, 20);
        });
        grip.addEventListener("dragend", () => {
            row.classList.remove("dragging");
            dragged = null;
            save();
            changed();
        });
    });

    // Пока тащим: ставим строку выше или ниже той, над которой мышь
    container.addEventListener("dragover", e => {
        if (!dragged) return;
        e.preventDefault(); // разрешить «бросить» здесь
        const target = e.target.closest(".lane");
        if (!target || target === dragged || target.parentElement !== container) return;
        const r = target.getBoundingClientRect();
        const after = e.clientY > r.top + r.height / 2;
        container.insertBefore(dragged, after ? target.nextElementSibling : target);
    });
    container.addEventListener("drop", e => { if (dragged) e.preventDefault(); });

    // Восстановить запомненный порядок
    try {
        const saved = JSON.parse(localStorage.getItem(storageKey) || "[]");
        if (Array.isArray(saved) && saved.length) apply(saved);
    } catch (e) { /* испорченная запись — остаёмся с исходным порядком */ }

    return {
        reset() {
            apply(defaultOrder);
            try { localStorage.removeItem(storageKey); } catch (e) { }
            changed();
        },
    };
}

// makeTimeAxis(lanesEl, width) — строка с подписями времени ПОД дорожками.
// Отдельно от дорожек, поэтому при любом порядке дорожек ось остаётся внизу.
function makeTimeAxis(lanesEl, width) {
    const row = document.createElement("div");
    row.className = "lane axisrow";
    const nameEl = document.createElement("div");
    const plotEl = document.createElement("div");
    const valEl = document.createElement("div");
    nameEl.className = "name";
    valEl.className = "val";
    row.append(nameEl, plotEl, valEl);
    lanesEl.after(row);

    return new uPlot({
        width,
        height: 34,
        legend: { show: false },
        cursor: { show: false, drag: { x: false, y: false } },
        scales: { y: { range: [0, 1] } }, // у оси нет данных — задаём шкалу Y явно
        axes: [
            { stroke: ISA.axis, size: 30, grid: { show: false }, ticks: { stroke: ISA.grid, width: 1 } },
            // Пустая ось Y той же ширины, что у дорожек, — чтобы время стояло ровно под графиками
            {
                size: 55, grid: { show: false }, ticks: { show: false },
                values: (u, splits) => splits.map(() => "")
            },
            // И справа — место под шкалу наложенного сигнала, как у дорожек
            {
                side: 1, size: 55, grid: { show: false }, ticks: { show: false },
                values: (u, splits) => splits.map(() => "")
            },
        ],
        series: [{}, { show: false }],
    }, [[0, 1], [null, null]], plotEl);
}

// mergeXY — два ряда с РАЗНЫМ временем (например, 10 мс и 100 мс) в один набор для uPlot.
// У uPlot одна ось X на график, поэтому берём объединение моментов времени,
// а там, где у ряда точки нет, ставим null (линия рисуется через пропуск — spanGaps).
// Оба ряда уже отсортированы по времени, поэтому хватает одного прохода «слиянием»,
// как при слиянии двух отсортированных списков.
function mergeXY(xa, ya, xb, yb) {
    const n = xa.length + xb.length;
    const x = new Array(n), a = new Array(n), b = new Array(n);
    let i = 0, j = 0, k = 0;
    while (i < xa.length || j < xb.length) {
        if (j >= xb.length || (i < xa.length && xa[i] < xb[j])) {
            x[k] = xa[i]; a[k] = ya[i]; b[k] = null; i++;
        } else if (i >= xa.length || xb[j] < xa[i]) {
            x[k] = xb[j]; a[k] = null; b[k] = yb[j]; j++;
        } else { // одно и то же время у обоих рядов
            x[k] = xa[i]; a[k] = ya[i]; b[k] = yb[j]; i++; j++;
        }
        k++;
    }
    x.length = a.length = b.length = k;
    return [x, a, b];
}

// overlayColor(baseIdx, srcIdx) — цвет наложенного сигнала: его собственный цвет,
// а если он совпал с цветом дорожки — следующий цвет палитры.
function overlayColor(baseIdx, srcIdx) {
    const n = ISA.traces.length;
    const c = srcIdx % n === baseIdx % n ? (srcIdx + 1) % n : srcIdx % n;
    return ISA.traces[c];
}

// laneSeries(color, ov) — серии uPlot для дорожки: основной сигнал сплошной линией,
// наложенный (если есть) — пунктиром, на своей шкале справа или на общей.
function laneSeries(color, ov) {
    const s = [{}, { stroke: color, width: 1, spanGaps: true }];
    if (ov) s.push({ stroke: ov.color, width: 1, dash: [6, 3], spanGaps: true, scale: ov.shared ? "y" : "y2" });
    return s;
}

// makeOverlays(container, key, onChange)
//   container — элемент со строками .lane (data-name = имя сигнала)
//   key       — под каким именем запомнить наложения (номер ПЛК)
//   onChange(name) — дорожку name надо перерисовать (поменялось наложение)
// После создания страница сама перерисовывает дорожки, у которых get(name) не null:
// изнутри makeOverlays это сделать нельзя — переменная страницы ещё не получила результат.
// Правила: на дорожку можно наложить ОДИН сигнал; наложенный сигнал прячется со своей
// дорожки; дорожку, на которую уже что-то наложено, саму наложить нельзя (без цепочек).
// Возвращает { get(name) → {name, shared} | null }.
function makeOverlays(container, key, onChange) {
    const storageKey = "plca:overlay:" + key;
    const rows = () => [...container.querySelectorAll(":scope > .lane")];
    const names = new Set(rows().map(r => r.dataset.name));

    // map: имя дорожки → { name: что наложено, shared: общая шкала }
    let map = {};
    try {
        const saved = JSON.parse(localStorage.getItem(storageKey) || "{}");
        if (saved && typeof saved === "object") map = saved;
    } catch (e) { /* испорченная запись — без наложений */ }

    // Чистим запомненное: сигналы могли удалить или переименовать
    const used = new Set();
    for (const [base, o] of Object.entries(map)) {
        const ok = o && names.has(base) && names.has(o.name) && o.name !== base &&
            !used.has(o.name) && !used.has(base); // без цепочек и повторов
        if (ok) { used.add(base); used.add(o.name); map[base] = { name: o.name, shared: !!o.shared }; }
        else delete map[base];
    }

    function isSource(n) { return Object.values(map).some(o => o.name === n); }

    function save() {
        try { localStorage.setItem(storageKey, JSON.stringify(map)); } catch (e) { }
    }

    // render — привести экран в соответствие с map: спрятать наложенные дорожки,
    // показать строку «+ имя» с кнопками ⇕ и ✕ у дорожек с наложением
    function render() {
        rows().forEach(r => {
            const n = r.dataset.name;
            const o = map[n];
            r.hidden = isSource(n);
            const add = r.querySelector(".ovl-add");
            if (add) add.hidden = !!o;

            const old = r.querySelector(".name .ovl");
            if (old) old.remove();
            if (!o) return;

            const line = document.createElement("div");
            line.className = "ovl";
            line.title = n + " + " + o.name + (o.shared ? " (общая шкала)" : " (шкала справа)");
            const sample = document.createElement("span");
            sample.className = "sample"; // пунктир — как на графике: различаем не только цветом
            const label = document.createElement("span");
            label.className = "label";
            label.textContent = "+ " + o.name;

            const shared = document.createElement("button");
            shared.textContent = o.shared ? "⇕" : "⇅";
            shared.title = o.shared
                ? "сейчас общая шкала — сделать свою шкалу справа"
                : "сейчас своя шкала справа — сделать общую";
            shared.onclick = () => set(n, { name: o.name, shared: !o.shared });

            const remove = document.createElement("button");
            remove.textContent = "✕";
            remove.title = "убрать наложение — сигнал вернётся на свою дорожку";
            remove.onclick = () => set(n, null);

            // Кнопки ПЕРЕД именем: длинное имя обрезается многоточием, а кнопки всегда на месте.
            // (Когда они стояли после имени, длинное имя выталкивало их под график,
            // и клик доставался графику, а не кнопке.)
            line.append(sample, shared, remove, label);
            r.querySelector(".name").append(line);
        });
    }

    function set(base, o) {
        const before = map[base];
        if (o) map[base] = o; else delete map[base];
        save();
        render();
        // Перерисовать дорожку и те, что спрятались или вернулись
        const touched = new Set([base, before && before.name, o && o.name].filter(Boolean));
        touched.forEach(n => onChange(n));
        window.dispatchEvent(new Event("resize")); // вернувшимся дорожкам — правильный размер
    }

    // Список для выбора: видимые дорожки без своего наложения, кроме этой
    function openPicker(row) {
        const base = row.dataset.name;
        const old = row.querySelector(".ovl-pick");
        if (old) { old.remove(); return; }
        const sel = document.createElement("select");
        sel.className = "ovl-pick";
        sel.append(new Option("— наложить сигнал —", ""));
        rows()
            .filter(r => r !== row && !r.hidden && !map[r.dataset.name])
            .forEach(r => sel.append(new Option(r.dataset.name, r.dataset.name)));
        sel.onchange = () => {
            const name = sel.value;
            // Удаление элемента снимает с него фокус и вызывает onblur, который тоже
            // удаляет список. Повторное удаление бросает исключение, и set() не выполнится.
            // Поэтому сначала отключаем onblur.
            sel.onblur = null;
            sel.remove();
            if (name) set(base, { name, shared: false });
        };
        sel.onblur = () => sel.remove();
        row.querySelector(".name").append(sel);
        sel.focus();
    }

    // Кнопка ⊕ рядом с ▲▼ (если перестановки нет — просто в колонке имени)
    rows().forEach(r => {
        const btn = document.createElement("button");
        btn.className = "ovl-add";
        btn.textContent = "⊕";
        btn.title = "наложить второй сигнал на эту дорожку";
        btn.onclick = () => openPicker(r);
        (r.querySelector(".name .ord") || r.querySelector(".name")).append(btn);
    });

    render();
    return { get: n => map[n] || null };
}