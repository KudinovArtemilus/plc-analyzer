// stats.js — кнопка «Σ Статистика» на странице просмотра записи.
// Использует exportRange и query из view.html. Считает сервер — по всем точкам файла.
(function () {
    const $ = id => document.getElementById(id);

    const btn = document.createElement("button");
    btn.textContent = "Σ Статистика";
    btn.title = "мин / макс / среднее и включения битов — между курсорами A–B или на видимом участке";
    $("export").appendChild(btn);

    const panel = $("stats");
    let open = false;
    let timer = null;
    let reqNo = 0;

    async function load() {
        const r = exportRange();
        if (!r) return;
        const no = ++reqNo;
        const resp = await fetch("ui/history/stats?" + query({
            from: Math.floor(r.from * 1000),
            to: Math.ceil(r.to * 1000),
        }));
        if (no !== reqNo) return; // пока ждали, курсор передвинули — этот ответ устарел
        // Сервер отдаёт HTML из html/template: текст сигналов уже экранирован
        panel.innerHTML = resp.ok ? await resp.text()
            : '<span class="err">ошибка: ' + (await resp.text()).replace(/</g, "&lt;") + "</span>";
        panel.hidden = false;
    }

    function close() {
        open = false;
        panel.hidden = true;
        panel.textContent = "";
    }

    btn.onclick = () => {
        if (open) { close(); return; }
        open = true;
        load();
    };

    // Крестик приходит вместе с таблицей и пересоздаётся при каждом обновлении,
    // поэтому слушаем щелчки на панели целиком
    panel.addEventListener("click", e => {
        if (e.target.closest(".stats-close")) close();
    });

    // view.html вызывает при каждом сдвиге курсора или зуме. Пока панель открыта —
    // пересчитываем, но не на каждое движение, а через 300 мс после последнего
    window.statsChanged = () => {
        if (!open) return;
        clearTimeout(timer);
        timer = setTimeout(load, 300);
    };
})();