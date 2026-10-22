// snapshot.js — кнопка «📌 Слепок» на странице просмотра.
// Использует viewer, exportRange, fmtDur, file, names, plcQ из view.html.
(function () {
  const $ = id => document.getElementById(id);

  const btn = document.createElement("button");
  btn.textContent = "📌 Слепок";
  $("export").appendChild(btn);

  const panel = document.createElement("div");
  panel.hidden = true;
  panel.className = "panel-form";
  panel.innerHTML = `
    <b>Сохранить слепок</b> <span id="snapRange" style="color:#555"></span><br>
    <input id="snapName" placeholder="название, например «Авария клещей 21.10»" size="44">
    <input id="snapCat" placeholder="категория" list="snapCats" size="24">
    <datalist id="snapCats"></datalist><br>
    <textarea id="snapComment" placeholder="комментарий: что произошло, что заметили" rows="2" cols="72"></textarea><br>
    <button id="snapSave">Сохранить</button>
    <button id="snapCancel">Отмена</button>
    <span id="snapMsg"></span>`;
  $("toolbar").after(panel);

  function msg(text, ok) {
    $("snapMsg").textContent = text;
    $("snapMsg").style.color = ok ? "#1b5e20" : "#b71c1c";
  }

  btn.onclick = async () => {
    const r = exportRange();
    if (!r) return;
    panel.hidden = false;
    msg("", true);
    $("snapRange").textContent = `(${r.label}, ${fmtDur(r.to - r.from)}, сигналов: ${names.length})`;

    const cats = await (await fetch("api/snapshots/categories" + plcQ)).json();
    const dl = $("snapCats");
    dl.textContent = "";
    cats.forEach(c => {
      const o = document.createElement("option");
      o.value = c;
      dl.appendChild(o);
    });
    $("snapName").focus();
  };

  $("snapCancel").onclick = () => { panel.hidden = true; };

  $("snapSave").onclick = async () => {
    const r = exportRange();
    if (!r) return;
    const body = {
      file, signals: names,
      from_ms: Math.floor(r.from * 1000),
      to_ms:   Math.ceil(r.to * 1000),
      name:     $("snapName").value,
      category: $("snapCat").value,
      comment:  $("snapComment").value,
    };
    msg("сохраняю…", true);
    const resp = await fetch("api/snapshots" + plcQ, {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify(body),
    });
    if (!resp.ok) {
      msg("ошибка: " + await resp.text(), false);
      return;
    }
    msg("слепок сохранён ✓ — он есть на странице «Слепки»", true);
    $("snapName").value = $("snapComment").value = "";
  };
})();
