"use strict";
(function () {
  // All captured text is set with textContent, never parsed as HTML.
  var D = JSON.parse(document.getElementById("afr-data").textContent);
  var RANK = { high: 3, medium: 2, low: 1 };

  function el(tag, cls, text) {
    var e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined && text !== null) e.textContent = String(text);
    return e;
  }
  function plural(n, w) { return n + " " + w + (n === 1 ? "" : "s"); }
  function short(id) { return id.length > 8 ? id.slice(0, 8) : id; }
  function badge(m) {
    return el("span", "badge s-" + m.sev, m.sev.toUpperCase() + " " + m.rule + (m.current ? "*" : ""));
  }
  function maxRank(line) {
    var r = 0;
    (line.marks || []).forEach(function (m) { r = Math.max(r, RANK[m.sev] || 0); });
    return r;
  }

  document.getElementById("meta").textContent =
    plural(D.sessions.length, "session") + " · times in " + D.zone + " · generated " + D.generated +
    " by afr " + D.version + (D.scope ? " · " + D.scope : "");

  // A collapsible node: header row plus children drawn on first open.
  function node(cls, fill) {
    var n = el("div", "node " + cls), hd = el("div", "hd"), tg = el("span", "tg", "+"), ch = el("div", "ch");
    hd.appendChild(tg);
    n.appendChild(hd);
    n.appendChild(ch);
    n.filled = !fill;
    n.setOpen = function (open) {
      if (open && !n.filled) { n.filled = true; fill(ch); }
      n.classList.toggle("open", open);
      tg.textContent = open ? "−" : "+";
    };
    hd.addEventListener("click", function () { n.setOpen(!n.classList.contains("open")); });
    n.hd = hd;
    n.ch = ch;
    return n;
  }

  // ---- sessions tree: date, then user, then session ----
  var tree = document.getElementById("tree"), byDate = {}, sess = [];
  D.sessions.forEach(function (s, i) {
    s.index = i;
    s.marks = [];
    s.lines.forEach(function (l, j) {
      (l.marks || []).forEach(function (m) { s.marks.push({ m: m, line: j }); });
    });
    (byDate[s.date] = byDate[s.date] || {});
    (byDate[s.date][s.user] = byDate[s.date][s.user] || []).push(s);
  });
  if (!D.sessions.length) tree.appendChild(el("p", "empty", "No sessions matched."));

  function sevCounts(list) {
    var c = { high: 0, medium: 0, low: 0 };
    list.forEach(function (x) { if (c[x.m.sev] !== undefined) c[x.m.sev]++; });
    return c;
  }
  function addCounts(parent, c) {
    ["high", "medium", "low"].forEach(function (k) {
      if (c[k]) parent.appendChild(el("span", "badge s-" + k, c[k] + " " + k));
    });
  }

  Object.keys(byDate).sort().reverse().forEach(function (date) {
    var users = byDate[date], all = [];
    Object.keys(users).forEach(function (u) { all = all.concat(users[u]); });
    var dn = node("date");
    dn.hd.appendChild(el("span", "", date));
    dn.hd.appendChild(el("span", "meta", plural(Object.keys(users).length, "user") + ", " + plural(all.length, "session")));
    var dm = [];
    all.forEach(function (s) { dm = dm.concat(s.marks); });
    addCounts(dn.hd, sevCounts(dm));
    tree.appendChild(dn);
    dn.sessNodes = [];
    Object.keys(users).sort().forEach(function (user) {
      var list = users[user].sort(function (a, b) { return a.start < b.start ? -1 : 1; });
      var un = node("user");
      un.hd.appendChild(el("span", "", user || "(unknown user)"));
      un.hd.appendChild(el("span", "meta", plural(list.length, "session")));
      var um = [];
      list.forEach(function (s) { um = um.concat(s.marks); });
      addCounts(un.hd, sevCounts(um));
      dn.ch.appendChild(un);
      list.forEach(function (s) {
        var sn = node("sess", function (ch) { fillSession(s, ch); });
        s.node = sn;
        s.parents = [dn, un];
        sn.hd.appendChild(el("span", "id", short(s.id)));
        var meta = s.start + " · " + s.host + " · " + plural(s.turns, "turn") + " · " + plural(s.tools, "tool call");
        sn.hd.appendChild(el("span", "meta", meta));
        addCounts(sn.hd, sevCounts(s.marks));
        if (s.denied) sn.hd.appendChild(el("span", "badge deny", plural(s.denied, "denial")));
        if (s.failed) sn.hd.appendChild(el("span", "badge fail", plural(s.failed, "failed call")));
        if (s.notices) sn.hd.appendChild(el("span", "badge warn", plural(s.notices, "notice")));
        if (s.open) sn.hd.appendChild(el("span", "badge warn", "open"));
        sn.hd.appendChild(el("span", "badge " + (s.integrity_ok ? "ok" : "bad"), s.integrity_ok ? "verified" : "integrity: " + s.integrity));
        un.ch.appendChild(sn);
        sess.push(s);
      });
    });
  });

  function fact(t, k, v) {
    if (v === undefined || v === null || v === "") return;
    var tr = el("tr");
    tr.appendChild(el("th", "", k));
    tr.appendChild(el("td", "", v));
    t.appendChild(tr);
  }

  function fillSession(s, ch) {
    var t = el("table", "facts");
    fact(t, "Session", s.id);
    fact(t, "User", s.user + " on " + s.host);
    fact(t, "Folder", s.cwd);
    fact(t, "Agent", s.agent);
    fact(t, "Models", (s.models || []).join(", "));
    fact(t, "Started", s.date + " " + s.start);
    fact(t, "Last event", s.last);
    fact(t, "Status", s.status);
    fact(t, "Integrity", s.integrity_ok ? "ok: hash chain and content hashes verify" : s.integrity);
    if (s.current) fact(t, "Note", plural(s.current, "highlight") + " marked * come from the current rules, not from when the session was recorded");
    ch.appendChild(t);
    s.lineEls = s.lines.map(function (l) { var e = lineEl(l); ch.appendChild(e); return e; });
    applyFilter(s);
  }

  function lineEl(l) {
    var e = el("div", "ln"), hd = el("div", "hd");
    hd.appendChild(el("span", "t", l.time));
    hd.appendChild(el("span", "k k-" + l.kind.replace(/ /g, "_"), l.kind));
    hd.appendChild(el("span", "tn", l.turn || "-"));
    var x = el("span", "x");
    (l.marks || []).forEach(function (m) { x.appendChild(badge(m)); });
    if (l.agent_type) x.appendChild(el("span", "badge", l.agent_type));
    if (l.tool) {
      x.appendChild(document.createTextNode(l.tool.name + "  " + l.tool.summary + "  →  "));
      x.appendChild(el("span", l.tool.denied ? "deny" : l.tool.failed ? "fail" : "", l.tool.status));
    } else {
      x.appendChild(document.createTextNode(l.text));
    }
    hd.appendChild(x);
    e.appendChild(hd);
    var dt = el("div", "dt"), drawn = false;
    e.appendChild(dt);
    hd.addEventListener("click", function () {
      if (!drawn) {
        drawn = true;
        (l.marks || []).forEach(function (m) {
          if (m.detail) {
            var p = el("p", "mk");
            p.appendChild(badge(m));
            p.appendChild(document.createTextNode(m.detail));
            dt.appendChild(p);
          }
        });
        (l.details || []).forEach(function (d) {
          dt.appendChild(el("div", "lbl", d.label));
          dt.appendChild(el("pre", "", d.body));
        });
      }
      e.classList.toggle("open");
    });
    return e;
  }

  // ---- triage ----
  function reveal(s, j) {
    // A triage click shows its line whatever the current search hides.
    if (active()) { q.value = ""; sev.value = ""; run(); }
    s.parents.forEach(function (p) { p.setOpen(true); });
    s.node.setOpen(true);
    var e = s.lineEls && s.lineEls[j];
    if (!e) return;
    e.classList.remove("hidden");
    e.scrollIntoView({ block: "center" });
    e.classList.add("flash");
    setTimeout(function () { e.classList.remove("flash"); }, 1500);
  }

  var flags = document.getElementById("flags"), flagged = 0;
  sess.forEach(function (s) {
    var why = [];
    if (!s.integrity_ok) why.push(["bad", "integrity: " + s.integrity]);
    if (s.denied) why.push(["deny", plural(s.denied, "call") + " denied by auto mode"]);
    if (s.notices) why.push(["warn", plural(s.notices, "harness notice")]);
    if (s.open) why.push(["warn", "still open: no session end recorded"]);
    if (!why.length) return;
    flagged++;
    var r = el("div", "row");
    r.appendChild(el("span", "where", s.date + " " + s.start + " " + short(s.id) + " " + s.user));
    why.forEach(function (w) { r.appendChild(el("span", "badge " + w[0], w[1])); });
    r.addEventListener("click", function () { reveal(s, -1); s.node.hd.scrollIntoView({ block: "center" }); });
    flags.appendChild(r);
  });

  if (!flagged) flags.appendChild(el("p", "empty", "None: every session ended, verifies, and has no denials or notices."));
  var marks = document.getElementById("marks"), all = [];
  sess.forEach(function (s) { s.marks.forEach(function (x) { all.push({ s: s, m: x.m, line: x.line }); }); });
  all.sort(function (a, b) {
    return (RANK[b.m.sev] || 0) - (RANK[a.m.sev] || 0) ||
      (a.s.date + a.s.lines[a.line].time < b.s.date + b.s.lines[b.line].time ? -1 : 1);
  });
  if (!all.length) marks.appendChild(el("p", "empty", "No highlights in these sessions."));
  if (all.some(function (x) { return x.m.current; }))
    marks.appendChild(el("p", "mut", "Highlights marked * come from the current rules, not from when the session was recorded."));
  all.forEach(function (x) {
    var l = x.s.lines[x.line], r = el("div", "row");
    r.appendChild(badge(x.m));
    r.appendChild(el("span", "where", x.s.date + " " + l.time + " " + short(x.s.id) + " " + x.s.user));
    r.appendChild(el("span", "what", x.m.detail || (l.tool ? l.tool.name + " " + l.tool.summary : l.text)));
    r.addEventListener("click", function () { reveal(x.s, x.line); });
    marks.appendChild(r);
  });

  // ---- search and severity filter ----
  var q = document.getElementById("q"), sev = document.getElementById("sev"), count = document.getElementById("count");
  function hay(l) {
    if (l.hay === undefined) {
      var parts = [l.kind, l.text, l.agent_type || ""];
      (l.marks || []).forEach(function (m) { parts.push(m.rule, m.sev, m.detail || ""); });
      (l.details || []).forEach(function (d) { parts.push(d.body); });
      l.hay = parts.join("\n").toLowerCase();
    }
    return l.hay;
  }
  function matches(l) {
    var term = q.value.trim().toLowerCase(), min = RANK[sev.value] || 0;
    if (min && maxRank(l) < min) return false;
    return !term || hay(l).indexOf(term) >= 0;
  }
  function active() { return q.value.trim() !== "" || sev.value !== ""; }
  function applyFilter(s) {
    if (!s.lineEls) return;
    var on = active();
    s.lines.forEach(function (l, j) { s.lineEls[j].classList.toggle("hidden", on && !matches(l)); });
  }
  var timer;
  function run() {
    var on = active(), lines = 0, hit = 0;
    document.querySelectorAll(".node.date,.node.user").forEach(function (n) { n.classList.toggle("hidden", on); });
    sess.forEach(function (s) {
      var n = 0;
      if (on) s.lines.forEach(function (l) { if (matches(l)) n++; });
      lines += n;
      if (n) hit++;
      s.node.classList.toggle("hidden", on && !n);
      if (on && n) {
        s.parents.forEach(function (p) { p.classList.remove("hidden"); p.setOpen(true); });
        s.node.setOpen(true);
      }
      applyFilter(s);
    });
    count.textContent = on ? plural(lines, "line") + " in " + plural(hit, "session") : "";
  }
  q.addEventListener("input", function () { clearTimeout(timer); timer = setTimeout(run, 150); });
  sev.addEventListener("change", run);
  document.getElementById("expand").addEventListener("click", function () {
    document.querySelectorAll(".node").forEach(function (n) { if (!n.classList.contains("hidden")) n.setOpen(true); });
  });
  document.getElementById("collapse").addEventListener("click", function () {
    document.querySelectorAll(".node").forEach(function (n) { n.setOpen(false); });
  });
})();
