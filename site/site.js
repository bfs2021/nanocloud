// The page works without this file: every section reads in full, the sample
// host shows its VMs running, and the download link points at Linux.
(() => {
  const reduced = matchMedia("(prefers-reduced-motion: reduce)").matches;
  const $ = (sel, root = document) => root.querySelector(sel);
  const $$ = (sel, root = document) => [...root.querySelectorAll(sel)];

  // Entrances: each marked part rises in once, as its section arrives.
  const rise = new IntersectionObserver(
    (entries) => {
      for (const e of entries) {
        if (!e.isIntersecting) continue;
        e.target.classList.add("in");
        rise.unobserve(e.target);
      }
    },
    { rootMargin: "0px 0px -10% 0px" },
  );
  $$("[data-rise]").forEach((el) => rise.observe(el));

  // Hero reel: the screens pass through the middle of the sentence as the
  // page scrolls, one screen per step, while the sentence holds still.
  const reel = $("[data-reel]");
  const hero = $(".hero");
  if (reel && hero) {
    const frames = $$("figure", reel);
    const place = () => {
      if (innerWidth <= 860) return;
      const h = frames[0].getBoundingClientRect().height + 24;
      const run = hero.offsetHeight - innerHeight;
      const t =
        run > 0
          ? Math.min(1, Math.max(0, -hero.getBoundingClientRect().top / run))
          : 0;
      const pos = t * (frames.length - 1);
      const y = -(frames[0].getBoundingClientRect().height / 2) - pos * h;
      reel.style.transform = `translateY(${y}px)`;
    };
    addEventListener("scroll", () => requestAnimationFrame(place), {
      passive: true,
    });
    addEventListener("resize", place);
    frames[0].querySelector("img").decode?.().then(place, place);
    place();
  }

  // From nothing to a VM: the statement nearest the middle of the screen
  // decides which state the held fragment shows.
  const stage = $("[data-stage]");
  const steps = $$(".step");
  const rail = $$("[data-rail] span");
  const bootPill = $("[data-boot-pill]");
  let railTimer;
  const boot = () => {
    clearInterval(railTimer);
    if (reduced) return;
    let i = 0;
    const paint = () => {
      rail.forEach((s, j) => {
        s.className = j < i ? "done" : j === i ? "now" : "";
      });
      const ready = i >= rail.length;
      bootPill.className = `pill ${ready ? "ready" : "booting"}`;
      bootPill.textContent = ready ? "Ready" : "Booting";
      if (ready) clearInterval(railTimer);
      i++;
    };
    paint();
    railTimer = setInterval(paint, 700);
  };
  if (stage) {
    const pick = new IntersectionObserver(
      (entries) => {
        for (const e of entries) {
          if (!e.isIntersecting) continue;
          const n = e.target.dataset.step;
          steps.forEach((s) => s.classList.toggle("active", s === e.target));
          if (stage.dataset.step !== n) {
            stage.dataset.step = n;
            if (n === "3") boot();
          }
        }
      },
      { rootMargin: "-45% 0px -45% 0px" },
    );
    steps.forEach((s) => pick.observe(s));
  }

  // Copy buttons. Only the command lines are copied, one per line, so a
  // pasted command runs exactly as shown.
  $$("[data-copy]").forEach((btn) => {
    btn.addEventListener("click", async () => {
      const block = document.getElementById(btn.dataset.copy);
      const text = $$("[data-line]", block)
        .map((l) => l.textContent.replace(/\s+/g, " ").trim())
        .join("\n");
      try {
        await navigator.clipboard.writeText(text);
        btn.textContent = "Copied";
      } catch {
        getSelection().selectAllChildren(block);
        btn.textContent = "Press ⌘C";
      }
      setTimeout(() => (btn.textContent = "Copy"), 1800);
    });
  });

  // Install tabs: a Mac visitor sees the Mac command first.
  const tabs = $$("[data-tab]");
  if (tabs.length) {
    const select = (tab, focus) => {
      for (const t of tabs) {
        const on = t === tab;
        t.setAttribute("aria-selected", String(on));
        t.tabIndex = on ? 0 : -1;
        document.getElementById(t.getAttribute("aria-controls")).hidden = !on;
      }
      if (focus) tab.focus();
    };
    tabs.forEach((t, i) => {
      t.addEventListener("click", () => select(t));
      t.addEventListener("keydown", (e) => {
        const step = { ArrowRight: 1, ArrowLeft: -1 }[e.key];
        if (step) select(tabs[(i + step + tabs.length) % tabs.length], true);
      });
    });
    const mac =
      /Mac/.test(navigator.platform) || /Mac OS X/.test(navigator.userAgent);
    select(tabs[mac ? 1 : 0]);
  }

  host();

  // The sample host. It follows the daemon's own rules (internal/reconcile):
  // a tick every 10 seconds, up to 6 attempts with a back-off that doubles
  // from 10 seconds, "recovered" after a minute of running, a lost
  // definition rebuilt from the spec, and the event feed, which lives in
  // memory, starting empty after the host comes back. Time runs faster.
  function host() {
    const root = $("[data-host]");
    if (!root) return;
    const list = $("[data-vms]", root);
    const feed = $("[data-feed]", root);
    const state = $("[data-host-state]", root);
    const clock = $("[data-clock]", root);
    const buttons = Object.fromEntries(
      $$("[data-act]").map((b) => [b.dataset.act, b]),
    );

    // One speed for every scenario: a power cut settles in about 4 seconds
    // and a crash loop runs to "gave up" in about 30.
    const SPEED = 20;
    const TICK = 10;
    const MAX = 6;
    const STABLE = 60;
    const BOOT = 25; // seconds of guest boot shown as "Booting"
    const HOST_BOOT = 40; // power on to the daemon's first tick
    const backoff = (n) => Math.min(300, 10 * 2 ** (n - 1));

    let now = 0; // simulated seconds
    let power = true;
    let daemonAt = -Infinity; // when the daemon came up
    let lastTick = 0;
    let recovery = true;
    const start = 3 * 86400 + 4 * 3600;
    const vms = [
      "web-01",
      "db-main",
      "k8s-lab-server",
      "k8s-lab-agent-1",
      "cache",
    ].map((name) => ({
      name,
      on: true,
      defined: true,
      bootAt: -start,
      attempts: 0,
      nextAttempt: 0,
      gaveUp: false,
      broken: false,
    }));

    const t0 = new Date(2026, 10, 14, 9, 41, 0).getTime();
    const stamp = (s) => new Date(t0 + s * 1000).toTimeString().slice(0, 8);
    const span = (s) => {
      s = Math.max(0, Math.floor(s));
      if (s >= 86400)
        return `${Math.floor(s / 86400)}d ${Math.floor((s % 86400) / 3600)}h`;
      if (s >= 3600)
        return `${Math.floor(s / 3600)}h ${Math.floor((s % 3600) / 60)}m`;
      if (s >= 60) return `${Math.floor(s / 60)}m ${s % 60}s`;
      return `${s}s`;
    };

    const event = (vm, message, tone = "") => {
      $(".feed-empty", feed)?.remove();
      const li = document.createElement("li");
      li.innerHTML = `<time></time><span class="who"></span><span class="${tone}"></span>`;
      li.children[0].textContent = stamp(now);
      li.children[1].textContent = vm;
      li.children[2].textContent = message;
      feed.prepend(li);
      while (feed.children.length > 7) feed.lastElementChild.remove();
    };
    const clearFeed = () => {
      feed.innerHTML =
        '<li class="feed-empty">The daemon just started; its feed is empty.</li>';
    };

    const startVM = (vm) => {
      vm.on = true;
      vm.bootAt = now;
    };

    const tick = () => {
      if (!recovery) return;
      for (const vm of vms) {
        if (vm.gaveUp) continue;
        if (vm.on && vm.attempts > 0 && now - vm.bootAt >= STABLE) {
          vm.attempts = 0;
          event(vm.name, "recovered — running steadily for 1m0s", "good");
          continue;
        }
        if (vm.on || now < vm.nextAttempt) continue;
        if (vm.attempts >= MAX) {
          vm.gaveUp = true;
          event(vm.name, `gave up restarting after ${MAX} attempts`, "error");
          continue;
        }
        vm.attempts++;
        vm.nextAttempt = now + backoff(vm.attempts);
        if (!vm.defined) {
          vm.defined = true;
          startVM(vm);
          event(
            vm.name,
            `rebuilt from its spec and started (attempt ${vm.attempts}/${MAX})`,
          );
        } else {
          startVM(vm);
          event(vm.name, `restarted (attempt ${vm.attempts}/${MAX})`);
        }
      }
    };

    const phase = (vm) => {
      if (!vm.defined) return ["missing", "Missing"];
      if (vm.gaveUp) return ["gave-up", "Gave up"];
      if (!vm.on)
        return vm.attempts > 0
          ? ["crash-looping", "Crash-looping"]
          : ["stopped", "Stopped"];
      if (now - vm.bootAt < BOOT) return ["booting", "Booting"];
      return ["ready", "Ready"];
    };

    const render = () => {
      list.innerHTML = "";
      for (const vm of vms) {
        const [cls, label] = phase(vm);
        const li = document.createElement("li");
        li.className = "vm-row";
        li.innerHTML =
          '<span class="vm-name"></span><span></span><span class="vm-up"></span>';
        li.children[0].textContent = vm.name;
        li.children[1].className = `pill ${cls}`;
        li.children[1].textContent = label;
        li.children[2].textContent = vm.on ? span(now - vm.bootAt) : "–";
        list.append(li);
      }
      root.dataset.power = power ? "on" : "off";
      const daemonUp = power && now >= daemonAt;
      state.textContent = !power
        ? "No power"
        : !daemonUp
          ? "Starting up…"
          : recovery
            ? `Up ${span(now - (daemonAt === -Infinity ? -start : daemonAt))}`
            : "Up · recovery is off";
      clock.textContent = stamp(now);
      buttons.power.textContent = power ? "Pull the plug" : "Power on";
      buttons.power.classList.toggle("danger", power);
      buttons.crash.disabled = !daemonUp || !vms[1].on;
      buttons.undefine.disabled = !daemonUp || !vms[0].defined;
      buttons.break.textContent = vms[4].broken ? "Fix it" : "Break it";
      buttons.break.disabled = !daemonUp;
      buttons.recovery.setAttribute("aria-checked", String(recovery));
    };

    const step = (dt) => {
      const end = now + dt;
      while (now < end) {
        now = Math.min(end, now + 1);
        if (!power) continue;
        for (const vm of vms) {
          if (vm.broken && vm.on && now - vm.bootAt >= 8) vm.on = false;
        }
        if (now >= daemonAt && now - lastTick >= TICK) {
          lastTick = now;
          tick();
        }
      }
    };

    buttons.power.addEventListener("click", () => {
      power = !power;
      if (!power) {
        for (const vm of vms) vm.on = false;
      } else {
        daemonAt = now + HOST_BOOT;
        lastTick = daemonAt - TICK;
        for (const vm of vms) {
          vm.attempts = 0;
          vm.nextAttempt = 0;
        }
        clearFeed();
      }
      render();
    });
    buttons.recovery.addEventListener("click", () => {
      recovery = !recovery;
      render();
    });
    buttons.crash.addEventListener("click", () => {
      vms[1].on = false;
      render();
    });
    buttons.undefine.addEventListener("click", () => {
      Object.assign(vms[0], { on: false, defined: false });
      render();
    });
    buttons.break.addEventListener("click", () => {
      const vm = vms[4];
      vm.broken = !vm.broken;
      // Fixing a VM that gave up is the operator's move: start it by hand.
      if (!vm.broken && (vm.gaveUp || !vm.on)) {
        Object.assign(vm, { gaveUp: false, attempts: 0, nextAttempt: 0 });
        startVM(vm);
      }
      render();
    });
    Object.values(buttons).forEach((b) => (b.disabled = false));

    // Runs only while the panel is on screen.
    let timer;
    new IntersectionObserver(([e]) => {
      clearInterval(timer);
      if (e.isIntersecting)
        timer = setInterval(() => {
          step(SPEED / 10);
          render();
        }, 100);
    }).observe(root);
    render();
  }
})();
