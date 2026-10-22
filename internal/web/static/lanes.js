// lanes.js — общее для дорожек на всех страницах:
//   makeSortable — перестановка дорожек (перетаскивание за ⠿ и кнопки ▲▼), порядок запоминается;
//   makeTimeAxis — отдельная строка с осью времени, всегда под дорожками.
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

    function move(row, dir) {
        if (dir < 0 && row.previousElementSibling) {
            container.insertBefore(row, row.previousElementSibling);
        } else if (dir > 0 && row.nextElementSibling) {
            container.insertBefore(row.nextElementSibling, row);
        } else {
            return;
        }
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
        ],
        series: [{}, { show: false }],
    }, [[0, 1], [null, null]], plotEl);
}