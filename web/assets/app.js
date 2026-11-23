/* GSTR-2B reconciler — interface logic. Talks only to this binary, on this
   machine, over fetch to its own origin. */
(function () {
  "use strict";

  /* ---------------------------------------------------------- small helpers */

  var root = document.documentElement;
  function $(id) { return document.getElementById(id); }

  var inr = new Intl.NumberFormat("en-IN", {
    style: "currency", currency: "INR",
    minimumFractionDigits: 2, maximumFractionDigits: 2
  });
  function money(paise) {
    var n = Number(paise);
    if (!isFinite(n)) { n = 0; }
    return inr.format(n / 100);
  }
  function dateText(iso) {
    if (!iso) { return ""; }
    var m = /^(\d{4})-(\d{2})-(\d{2})/.exec(String(iso));
    return m ? m[3] + "-" + m[2] + "-" + m[1] : String(iso);
  }
  function when(iso) {
    if (!iso) { return ""; }
    var d = new Date(iso);
    return isNaN(d.getTime()) ? String(iso) : d.toLocaleString();
  }
  function el(tag, cls, text) {
    var n = document.createElement(tag);
    if (cls) { n.className = cls; }
    if (text !== undefined && text !== null) { n.textContent = String(text); }
    return n;
  }
  function clear(node) {
    while (node.firstChild) { node.removeChild(node.firstChild); }
  }
  function count(n, one, many) {
    return n + " " + (n === 1 ? one : many);
  }

  /* ----------------------------------------------------------- theme toggle */

  function currentTheme() {
    var t = root.getAttribute("data-theme");
    if (t) { return t; }
    return (window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches) ? "dark" : "light";
  }
  var themer = document.querySelector("[data-themer]");
  function paintThemer() {
    if (!themer) { return; }
    var dark = currentTheme() === "dark";
    themer.textContent = dark ? "☀ Light" : "☾ Dark";
    themer.setAttribute("aria-label", dark ? "Switch to light theme" : "Switch to dark theme");
  }
  if (themer) {
    paintThemer();
    themer.addEventListener("click", function () {
      var next = currentTheme() === "dark" ? "light" : "dark";
      root.setAttribute("data-theme", next);
      try { localStorage.setItem("omsme-theme", next); } catch (e) { /* storage may be blocked */ }
      paintThemer();
    });
  }

  /* --------------------------------------------------------------- the API */

  function api(path, options) {
    return fetch(path, options).then(function (res) {
      var type = res.headers.get("content-type") || "";
      if (type.indexOf("application/json") < 0) {
        if (!res.ok) { throw new Error("the tool replied with status " + res.status); }
        return res.text();
      }
      return res.json().then(function (body) {
        if (!res.ok) { throw new Error((body && body.error) || ("status " + res.status)); }
        return body;
      });
    });
  }

  /* --------------------------------------------------------- category views */

  var CATEGORIES = [
    { id: "in-books-not-in-2b", label: "In books, not in 2B", field: "inBooksNotIn2B", kind: "atrisk",
      note: "Input tax credit can only be claimed on an invoice that appears in GSTR-2B. These are the invoices to chase." },
    { id: "in-2b-not-in-books", label: "In 2B, not in books", field: "in2BNotInBooks", kind: "twob",
      note: "Your supplier has reported these against your GSTIN but your register has no such entry. Either the purchase is unrecorded, or it is not yours." },
    { id: "value-mismatch", label: "Values differ", field: "matchedWithValueMismatch", kind: "mismatch",
      note: "The same invoice on both sides, with figures that differ by more than the tolerance." },
    { id: "duplicate-in-books", label: "Duplicates in books", field: "duplicateInBooks", kind: "dup",
      note: "The same supplier, invoice number and financial year recorded more than once in your register. Claiming each copy would over-claim the credit." },
    { id: "matched", label: "Matched", field: "matched", kind: "pair",
      note: "Present on both sides, with every figure within the tolerance. Nothing to do." },
    { id: "suppliers", label: "Supplier rollup", field: "suppliers", kind: "sup",
      note: "The invoices missing from GSTR-2B, grouped by supplier, largest amount at stake first." }
  ];

  var VIEWS = {
    atrisk: {
      head: ["Supplier GSTIN", "Supplier", "Invoice no", "Date", "Taxable value", "ITC at risk", "Line"],
      row: function (i) {
        return [
          [i.supplierGstin, "mono"], [i.supplierName || "—", ""], [i.invoiceNo, "mono"],
          [dateText(i.invoiceDate), "num"], [money(i.taxableValue), "num"],
          [money(tax(i)), "num risk"], [i.line ? String(i.line) : "—", "num"]
        ];
      }
    },
    twob: {
      head: ["Supplier GSTIN", "Supplier", "Invoice no", "Date", "Taxable value", "Tax in GSTR-2B"],
      row: function (i) {
        return [
          [i.supplierGstin, "mono"], [i.supplierName || "—", ""], [i.invoiceNo, "mono"],
          [dateText(i.invoiceDate), "num"], [money(i.taxableValue), "num"], [money(tax(i)), "num"]
        ];
      }
    },
    pair: {
      head: ["Supplier GSTIN", "Supplier", "Invoice no", "Date", "Taxable value", "Tax"],
      row: function (p) {
        var i = p.books || {};
        return [
          [i.supplierGstin, "mono"], [i.supplierName || "—", ""], [i.invoiceNo, "mono"],
          [dateText(i.invoiceDate), "num"], [money(i.taxableValue), "num"], [money(tax(i)), "num"]
        ];
      }
    },
    mismatch: {
      head: ["Supplier GSTIN", "Invoice no", "Date", "What differs", "GSTR-2B", "Your books", "Difference"],
      row: function (m) {
        var b = m.books || {}, t = m.twoB || {};
        var heads = (m.deltas || []).map(function (d) { return d.head; }).join(", ");
        var diff = Number(m.taxDelta) || 0;
        return [
          [b.supplierGstin, "mono"], [b.invoiceNo, "mono"], [dateText(b.invoiceDate), "num"],
          [heads, ""], [money(tax(t)), "num"], [money(tax(b)), "num"],
          [(diff > 0 ? "+" : "") + money(diff), "num" + (diff !== 0 ? " risk" : "")]
        ];
      }
    },
    dup: {
      head: ["Supplier GSTIN", "Invoice no", "Date", "Copies", "Lines", "Tax on one copy", "Tax on the extra copies"],
      row: function (g) {
        var invs = g.invoices || [];
        var first = invs[0] || {};
        var lines = invs.map(function (i) { return i.line || "?"; }).join(", ");
        return [
          [first.supplierGstin, "mono"], [first.invoiceNo, "mono"], [dateText(first.invoiceDate), "num"],
          [String(invs.length), "num"], [lines, "num mono"],
          [money(tax(first)), "num"], [money(g.extraTax), "num risk"]
        ];
      }
    },
    sup: {
      head: ["Supplier GSTIN", "Supplier", "Invoices missing", "ITC at risk"],
      row: function (s) {
        return [
          [s.gstin, "mono"], [s.name || "—", ""],
          [String((s.missing || []).length), "num"], [money(s.taxAtRisk), "num risk"]
        ];
      }
    }
  };

  function tax(i) {
    if (!i) { return 0; }
    return (Number(i.igst) || 0) + (Number(i.cgst) || 0) + (Number(i.sgst) || 0) + (Number(i.cess) || 0);
  }

  /* ----------------------------------------------------------- app state */

  var state = { run: null, category: CATEGORIES[0].id, supplier: 0 };

  function setStatus(text) {
    $("status").textContent = text;
  }
  function showError(text) {
    var box = $("error");
    if (!text) { box.hidden = true; box.textContent = ""; return; }
    box.textContent = text;
    box.hidden = false;
  }

  /* --------------------------------------------------------------- render */

  function renderSummary(run) {
    var res = run.result || {};
    var sum = res.summary || {};
    $("r-risk").textContent = money(res.totalAtRisk);

    var missing = (sum.onlyBooks && sum.onlyBooks.count) || 0;
    var sub = missing === 0
      ? "Every invoice in your register appears in GSTR-2B."
      : count(missing, "invoice", "invoices") + " in your books " + (missing === 1 ? "is" : "are") +
        " not in GSTR-2B, from " + count((res.suppliers || []).length, "supplier", "suppliers") + ".";
    $("r-sub").textContent = sub;

    function cell(id, total) {
      $(id).textContent = count((total && total.count) || 0, "invoice", "invoices") + " · " + money(total && total.tax);
    }
    cell("c-onlybooks", sum.onlyBooks);
    cell("c-only2b", sum.only2B);

    // For a value mismatch the useful figure is not the tax itself but how far
    // the books stand from GSTR-2B.
    var mismatches = res.matchedWithValueMismatch || [];
    var net = 0;
    mismatches.forEach(function (m) { net += Number(m.taxDelta) || 0; });
    var how = net > 0 ? " more tax than GSTR-2B" : (net < 0 ? " less tax than GSTR-2B" : " net nil");
    $("c-mismatch").textContent = count(mismatches.length, "invoice", "invoices") +
      (mismatches.length ? " · " + money(Math.abs(net)) + how : "");
    $("c-dups").textContent = count((sum.duplicates && sum.duplicates.count) || 0, "invoice", "invoices") +
      " · " + money(sum.duplicates && sum.duplicates.tax) + " extra";
    cell("c-matched", sum.matched);
    $("c-read").textContent = (sum.twoBInvoices || 0) + " in GSTR-2B · " + (sum.booksInvoices || 0) + " in your books";

    var files = [];
    if (run.twoBName) { files.push(run.twoBName); }
    if (run.booksName) { files.push(run.booksName); }
    var line = files.join("  +  ");
    if (run.periodText) { line += line ? "  ·  " + run.periodText : run.periodText; }
    if (res.tolerancePaise !== undefined) {
      line += (line ? "  ·  " : "") + "tolerance " + money(res.tolerancePaise) + " per head";
    }
    $("r-files").textContent = line;
  }

  function renderNotes(run) {
    var notes = (run.notes || []).concat((run.result && run.result.notes) || []);
    var box = $("notes");
    clear(box);
    if (!notes.length) { $("notes-card").hidden = true; return; }
    notes.forEach(function (n) { box.appendChild(el("div", "note", n)); });
    $("notes-card").hidden = false;
  }

  function categoryRows(run, cat) {
    var res = run.result || {};
    return res[cat.field] || [];
  }

  function renderTabs(run) {
    var tabs = $("tabs");
    clear(tabs);
    CATEGORIES.forEach(function (cat) {
      var b = el("button", null, cat.label);
      b.type = "button";
      b.setAttribute("aria-pressed", cat.id === state.category ? "true" : "false");
      var n = el("span", "n", categoryRows(run, cat).length);
      b.appendChild(n);
      b.addEventListener("click", function () {
        state.category = cat.id;
        renderTabs(run);
        renderTable(run);
      });
      tabs.appendChild(b);
    });
  }

  function renderTable(run) {
    var cat = CATEGORIES.filter(function (c) { return c.id === state.category; })[0] || CATEGORIES[0];
    var view = VIEWS[cat.kind];
    var rows = categoryRows(run, cat);

    $("cat-note").textContent = cat.note;

    var head = $("cat-head");
    clear(head);
    view.head.forEach(function (h, idx) {
      var th = el("th", idx >= 3 ? "num" : null, h);
      head.appendChild(th);
    });

    var body = $("cat-body");
    clear(body);
    if (!rows.length) {
      var tr = el("tr");
      var td = el("td", "empty", "Nothing in this category.");
      td.colSpan = view.head.length;
      tr.appendChild(td);
      body.appendChild(tr);
    } else {
      rows.forEach(function (item) {
        var tr = el("tr");
        view.row(item).forEach(function (pair) {
          tr.appendChild(el("td", pair[1] || null, pair[0]));
        });
        body.appendChild(tr);
      });
    }

    var link = $("x-csv");
    link.href = "/api/export?id=" + encodeURIComponent(run.id) + "&category=" + encodeURIComponent(cat.id);
    link.textContent = "Download “" + cat.label + "” as CSV";
    $("detail").hidden = false;
  }

  function renderFollowUp(run) {
    var suppliers = (run.result && run.result.suppliers) || [];
    var card = $("followup");
    if (!suppliers.length) { card.hidden = true; return; }
    if (state.supplier >= suppliers.length) { state.supplier = 0; }

    var tabs = $("sup-tabs");
    clear(tabs);
    suppliers.forEach(function (s, idx) {
      var b = el("button", null, s.name ? s.name : s.gstin);
      b.type = "button";
      b.setAttribute("aria-pressed", idx === state.supplier ? "true" : "false");
      b.appendChild(el("span", "n", money(s.taxAtRisk)));
      b.addEventListener("click", function () {
        state.supplier = idx;
        renderFollowUp(run);
      });
      tabs.appendChild(b);
    });

    var gstin = suppliers[state.supplier].gstin;
    $("msg").textContent = "Writing the message…";
    api("/api/followup?id=" + encodeURIComponent(run.id) + "&gstin=" + encodeURIComponent(gstin))
      .then(function (text) { $("msg").textContent = text; })
      .catch(function (err) { $("msg").textContent = "Could not write the message: " + err.message; });

    $("dl-msg").href = "/api/followup?download=1&id=" + encodeURIComponent(run.id) + "&gstin=" + encodeURIComponent(gstin);
    $("dl-all").href = "/api/followup?download=1&id=" + encodeURIComponent(run.id) + "&gstin=all";
    $("copy-status").textContent = "";
    card.hidden = false;
  }

  function render(run) {
    state.run = run;
    renderSummary(run);
    renderNotes(run);
    renderTabs(run);
    renderTable(run);
    renderFollowUp(run);
  }

  /* ----------------------------------------------------------- saved runs */

  function renderSaved(runs) {
    var card = $("saved");
    var body = $("saved-body");
    clear(body);
    if (!runs || !runs.length) {
      card.hidden = true;
      return;
    }
    runs.forEach(function (r) {
      var tr = el("tr");
      tr.appendChild(el("td", null, r.label ? r.label + " · " + when(r.createdAt) : when(r.createdAt)));
      tr.appendChild(el("td", null, [r.twoBName, r.booksName].filter(Boolean).join(" + ")));
      tr.appendChild(el("td", "num risk", money(r.totalAtRisk)));

      var td = el("td");
      var open = el("button", "btn ghost", "Open");
      open.type = "button";
      open.addEventListener("click", function () {
        setStatus("Opening that check…");
        api("/api/run?id=" + encodeURIComponent(r.id)).then(function (body) {
          render(body.run);
          setStatus("Showing the check from " + when(r.createdAt) + ".");
          showError("");
        }).catch(function (err) { showError(err.message); });
      });
      var del = el("button", "btn ghost", "Delete");
      del.type = "button";
      del.addEventListener("click", function () {
        api("/api/run?id=" + encodeURIComponent(r.id), { method: "DELETE" })
          .then(function () {
            if (state.run && state.run.id === r.id) {
              $("detail").hidden = true;
              $("followup").hidden = true;
            }
            return refreshState();
          })
          .catch(function (err) { showError(err.message); });
      });
      td.appendChild(open);
      td.appendChild(del);
      tr.appendChild(td);
      body.appendChild(tr);
    });
    $("saved-note").textContent = "Kept in the data file on this computer. The newest " + runs.length +
      " are listed; older ones are dropped as new checks are saved.";
    card.hidden = false;
  }

  function refreshState() {
    return api("/api/state").then(function (body) {
      var s = body.settings || {};
      if (s.tolerancePaise !== undefined && s.tolerancePaise !== null) {
        $("i-tolerance").value = (s.tolerancePaise / 100).toFixed(2);
      }
      if (s.businessName) { $("i-business").value = s.businessName; }
      if (s.contactLine) { $("i-contact").value = s.contactLine; }
      if (s.period) { $("i-period").value = s.period; }
      renderSaved(body.runs);
      return body;
    });
  }

  /* -------------------------------------------------------------- actions */

  function saveSettings() {
    return api("/api/settings", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({
        tolerance: $("i-tolerance").value || "0",
        businessName: $("i-business").value,
        contactLine: $("i-contact").value,
        period: $("i-period").value
      })
    });
  }

  function reconcile() {
    showError("");
    var twoBFile = $("f-2b").files && $("f-2b").files[0];
    var booksFile = $("f-books").files && $("f-books").files[0];
    var twoBPath = $("p-2b").value.trim();
    var booksPath = $("p-books").value.trim();

    var request;
    if (twoBFile && booksFile) {
      var form = new FormData();
      form.append("twob", twoBFile);
      form.append("books", booksFile);
      form.append("tolerance", $("i-tolerance").value || "0");
      form.append("label", $("i-label").value);
      request = { method: "POST", body: form };
    } else if (twoBPath && booksPath) {
      request = {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          twoBPath: twoBPath,
          booksPath: booksPath,
          tolerance: $("i-tolerance").value || "0",
          label: $("i-label").value
        })
      };
    } else {
      showError("Choose both files — the GSTR-2B JSON and your purchase register CSV — or give the path of each.");
      return;
    }

    var button = $("run");
    button.disabled = true;
    document.body.classList.add("busy");
    setStatus("Reading both files and matching invoice by invoice…");

    saveSettings()
      .then(function () { return api("/api/reconcile", request); })
      .then(function (body) {
        render(body.run);
        setStatus("Done. " + count((body.run.result.summary.booksInvoices || 0), "invoice", "invoices") +
          " in your register were checked against GSTR-2B.");
        return refreshState();
      })
      .catch(function (err) {
        showError(err.message);
        setStatus("The check did not run.");
      })
      .then(function () {
        button.disabled = false;
        document.body.classList.remove("busy");
      });
  }

  $("run").addEventListener("click", reconcile);

  $("reset").addEventListener("click", function () {
    ["f-2b", "f-books", "p-2b", "p-books", "i-label"].forEach(function (id) { $(id).value = ""; });
    $("detail").hidden = true;
    $("followup").hidden = true;
    $("notes-card").hidden = true;
    state.run = null;
    $("r-risk").textContent = money(0);
    $("r-sub").textContent = "Load the two files to check";
    ["c-onlybooks", "c-only2b", "c-mismatch", "c-dups", "c-matched", "c-read"].forEach(function (id) {
      $(id).textContent = "—";
    });
    $("r-files").textContent = "";
    showError("");
    setStatus("Cleared. Nothing was uploaded anywhere.");
  });

  $("copy").addEventListener("click", function () {
    var text = $("msg").textContent || "";
    if (!text) { return; }
    function selectIt() {
      try {
        var range = document.createRange();
        range.selectNodeContents($("msg"));
        var sel = window.getSelection();
        sel.removeAllRanges();
        sel.addRange(range);
        $("copy-status").textContent = "The message is selected — press Ctrl-C (or Cmd-C) to copy it.";
      } catch (e) {
        $("copy-status").textContent = "Select the message above and copy it.";
      }
    }
    if (navigator.clipboard && navigator.clipboard.writeText) {
      navigator.clipboard.writeText(text).then(function () {
        $("copy-status").textContent = "Copied. Paste it into your own email or messaging app and send it yourself.";
      }).catch(selectIt);
    } else {
      selectIt();
    }
  });

  refreshState().catch(function (err) {
    showError("Could not reach the tool: " + err.message);
  });
})();
