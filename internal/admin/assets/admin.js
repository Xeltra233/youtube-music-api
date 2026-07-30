(function () {
  "use strict";

  const SESSION_KEY = "ytmusic-managed-login-session";
  const ACTIVE_SESSION_STATES = new Set([
    "starting",
    "interactive",
    "verifying",
    "authenticated",
    "not_logged_in",
    "sync_failed",
  ]);
  const CONTROL_SESSION_STATES = new Set(["interactive", "not_logged_in", "sync_failed"]);

  const $ = (id) => document.getElementById(id);
  const clamp = (value, min, max) => Math.min(max, Math.max(min, value));

  let selectedFile = null;
  let cookieStatus = null;
  let loginSession = null;
  let loginSocket = null;
  let socketReady = false;
  let sessionPollTimer = 0;
  let frameURL = "";
  let hasFrame = false;
  let queuedPointerMove = null;
  let pointerAnimationFrame = 0;
  let resizeTimer = 0;
  let lastRequestedViewport = { width: 0, height: 0 };

  async function api(path, opts = {}) {
    const response = await fetch(path, {
      credentials: "same-origin",
      ...opts,
    });
    const payload = await response.json().catch(() => ({}));
    return { r: response, j: payload };
  }

  function apiError(payload, fallback) {
    return (payload && payload.error && payload.error.message) || fallback;
  }

  async function ensureAuth() {
    const { r, j } = await api("/api/admin/check-auth");
    if (!r.ok) return { ok: false, reason: "error" };
    if (j.enabled === false) return { ok: false, reason: "disabled" };
    if (!j.authenticated) {
      location.replace("./login.html");
      return { ok: false, reason: "unauthenticated" };
    }
    return { ok: true };
  }

  function fmtBytes(n) {
    n = Number(n) || 0;
    if (n < 1024) return n + " B";
    if (n < 1024 * 1024) return (n / 1024).toFixed(1) + " KB";
    return (n / (1024 * 1024)).toFixed(2) + " MB";
  }

  function sourceLabel(source) {
    return {
      managed: "服务浏览器",
      browser: "外部浏览器档案",
      file: "Cookie 文件",
      none: "匿名模式",
    }[source] || "未知";
  }

  function sessionStateLabel(state) {
    return {
      starting: "正在启动服务浏览器",
      interactive: "登录页面已连接",
      verifying: "正在校验登录状态",
      authenticated: "已识别登录状态",
      synced: "登录已保存并同步",
      not_logged_in: "等待完成 Google 登录",
      sync_failed: "Cookie 同步遇到问题",
      expired: "登录会话已过期",
      closed: "登录会话已结束",
    }[state] || "登录会话状态未知";
  }

  function loginErrorLabel(code) {
    return {
      managed_disabled: "当前来源模式未启用服务浏览器登录。",
      session_busy: "已有登录会话正在运行；如果刚刚刷新过页面，请尝试恢复原会话。",
      session_missing: "登录会话已经结束，请重新开始。",
      session_expired: "登录会话已过期，请重新开始。",
      browser_unavailable: "没有找到可用的 Chromium 浏览器，请检查浏览器路径或运行环境。",
      browser_start_failed: "服务浏览器启动失败，请重试；若持续出现，请检查浏览器路径与档案目录。",
      browser_closed: "服务浏览器已意外结束，请重新开始登录。",
      browser_starting: "服务浏览器仍在启动，请稍候。",
      control_busy: "这个登录会话已在另一个页面中打开。",
      verify_busy: "登录状态正在校验，请稍候。",
      not_logged_in: "还没有识别到 YouTube 登录状态，请在上方画面完成登录后再校验。",
      sync_failed: "登录 Cookie 同步未完成，请保留当前页面并重试校验。",
      cookie_export_failed: "浏览器登录状态读取失败，请保留当前页面并重试校验。",
      cookie_commit_failed: "登录 Cookie 保存失败，请保留当前页面并重试校验。",
      stable_jar_preserved: "已识别浏览器登录，但现有 Cookie 文件质量更高，因此继续保留现有文件。",
      input_rejected: "这次输入未被浏览器接收，请重新聚焦画面。",
      resize_rejected: "登录画面尺寸调整未生效。",
      message_rejected: "登录画面收到了一条无效操作。",
      origin_mismatch: "登录画面的来源校验未通过。",
    }[code] || "浏览器登录操作未完成，请重试。";
  }

  function setPill(text, tone) {
    const pill = $("managedStatePill");
    pill.textContent = text;
    pill.className = "status-pill " + tone;
  }

  function renderStatus(st) {
    cookieStatus = st;
    $("stPresent").textContent = st.present ? "可用" : "待配置";
    $("stPresent").style.color = st.present ? "var(--ok)" : "var(--muted)";
    $("stSource").textContent = sourceLabel(st.source);
    $("stLogin").textContent = st.logged_in ? "已登录" : "未登录";
    $("stLogin").style.color = st.logged_in ? "var(--ok)" : "var(--muted)";
    $("stCookieCount").textContent = String(Number(st.cookie_count) || 0);
    $("stQuality").textContent = st.valid ? String(Number(st.quality_score) || 0) : "-";
    $("stFile").textContent = st.active_file || "-";
    $("stMod").textContent = st.modified_at || "-";
    if (st.keepalive) {
      const sec = Number(st.keepalive_interval) || 0;
      const hrs = sec ? (sec / 3600).toFixed(sec % 3600 === 0 ? 0 : 1) : "?";
      $("stKeep").textContent = "开启 · 每 " + hrs + " 小时";
    } else {
      $("stKeep").textContent = "关闭";
    }
    $("stCount").textContent = String(Number(st.dropin_files) || 0);
    renderManagedSummary(st);
  }

  function renderManagedSummary(st) {
    const enabled = Boolean(st.managed_enabled);
    const authenticated = Boolean(st.managed_authenticated && st.logged_in);
    const sessionActive = loginSession && ACTIVE_SESSION_STATES.has(loginSession.state);
    const start = $("loginStartBtn");

    $("managedSource").textContent = sourceLabel(st.source);
    $("managedProfile").textContent = authenticated ? "已保存登录" : enabled ? "可开始登录" : "当前模式关闭";
    $("managedCookies").textContent = authenticated ? String(Number(st.auth_cookie_count) || 0) + " 个认证项" : "0 个认证项";
    $("managedQuality").textContent = st.valid ? String(Number(st.quality_score) || 0) : "-";

    if (authenticated) {
      setPill("YouTube 已登录", "is-ok");
      $("loginRouteNote").textContent = "服务专用浏览器档案正在作为主来源；外部浏览器档案同步会暂停，文件上传继续保留。";
    } else if (enabled) {
      setPill("等待浏览器登录", "is-warning");
      if (st.source === "browser") {
        $("loginRouteNote").textContent = "当前正在使用外部浏览器档案。服务浏览器登录是独立路线，可直接从这里开始。";
      } else if (st.source === "file") {
        $("loginRouteNote").textContent = "当前正在使用 Cookie 文件。完成服务浏览器登录后会自动切换到持久化档案。";
      } else {
        $("loginRouteNote").textContent = "服务浏览器会保存独立登录档案；无需先准备外部浏览器档案或 Cookie 文件。";
      }
    } else {
      setPill("服务浏览器已停用", "neutral");
      $("loginRouteNote").textContent = "当前 COOKIE_SOURCE_MODE 使用其他来源；可在运行配置中启用 managed 或 auto。";
    }

    start.disabled = !enabled || Boolean(sessionActive);
    start.textContent = sessionActive ? "登录进行中" : authenticated ? "重新登录" : "开始登录";
    $("managedDisconnectBtn").hidden = !authenticated;
    $("managedDisconnectBtn").disabled = Boolean(sessionActive);
  }

  async function refreshStatus() {
    const button = $("refreshStatusBtn");
    if (button) button.disabled = true;
    try {
      const { r, j } = await api("/api/admin/cookies/status", { cache: "no-store" });
      if (r.status === 401) {
        location.replace("./login.html");
        return null;
      }
      if (!r.ok) throw new Error(apiError(j, "状态获取失败"));
      renderStatus(j);
      return j;
    } finally {
      if (button) button.disabled = false;
    }
  }

  function clearLoginMessages() {
    $("loginErr").textContent = "";
    $("loginOk").textContent = "";
  }

  function showLoginError(message) {
    $("loginOk").textContent = "";
    $("loginErr").textContent = message || "浏览器登录操作未完成，请重试。";
  }

  function showLoginOK(message) {
    $("loginErr").textContent = "";
    $("loginOk").textContent = message || "操作完成";
  }

  function formatExpiry(value) {
    const expires = Date.parse(value || "");
    if (!Number.isFinite(expires)) return "会话有效期未知";
    const seconds = Math.max(0, Math.ceil((expires - Date.now()) / 1000));
    if (seconds <= 0) return "会话即将结束";
    const minutes = Math.floor(seconds / 60);
    const remain = seconds % 60;
    return minutes > 0 ? "剩余约 " + minutes + " 分 " + remain + " 秒" : "剩余约 " + remain + " 秒";
  }

  function showOverlay(title, text, spinning, tone) {
    const overlay = $("loginOverlay");
    overlay.hidden = false;
    overlay.className = "surface-overlay" + (tone ? " " + tone : "");
    $("loginOverlayTitle").textContent = title;
    $("loginOverlayText").textContent = text || "";
    $("loginSpinner").hidden = !spinning;
  }

  function hideOverlay() {
    $("loginOverlay").hidden = true;
  }

  function clearFrame() {
    if (frameURL) URL.revokeObjectURL(frameURL);
    frameURL = "";
    hasFrame = false;
    const image = $("loginFrame");
    image.hidden = true;
    image.removeAttribute("src");
  }

  function displayFrame(blob) {
    if (!(blob instanceof Blob) || blob.size === 0) return;
    const previous = frameURL;
    const next = URL.createObjectURL(blob);
    const image = $("loginFrame");
    image.onload = () => {
      if (previous) URL.revokeObjectURL(previous);
      if (frameURL !== next) {
        URL.revokeObjectURL(next);
        return;
      }
      hasFrame = true;
      image.hidden = false;
      if (loginSession && CONTROL_SESSION_STATES.has(loginSession.state)) hideOverlay();
    };
    image.onerror = () => {
      URL.revokeObjectURL(next);
      if (frameURL === next) frameURL = "";
      showOverlay("登录画面暂时不可用", "请尝试重连画面", false, "is-error");
    };
    frameURL = next;
    image.src = next;
  }

  function updateViewport(viewport) {
    const width = Number(viewport && viewport.width) || 0;
    const height = Number(viewport && viewport.height) || 0;
    if (width >= 320 && height >= 240) {
      $("loginSurface").style.setProperty("--login-aspect", String(width / height));
    }
  }

  function updateControlAvailability() {
    const controllable = Boolean(loginSession && CONTROL_SESSION_STATES.has(loginSession.state));
    const connected = controllable && socketReady;
    $("loginVerifyBtn").disabled = !controllable || loginSession.state === "verifying";
    $("loginReconnectBtn").hidden = !controllable || socketReady;
    $("loginTextInput").disabled = !connected;
    $("loginTextToggle").disabled = !connected;
    $("loginTextSendBtn").disabled = !connected;
  }

  function renderSession(session) {
    if (!session || !session.id) return;
    loginSession = session;
    const workspace = $("loginWorkspace");
    workspace.hidden = false;
    workspace.classList.toggle("is-terminal", ["synced", "closed", "expired"].includes(session.state));
    updateViewport(session.viewport);

    const active = ACTIVE_SESSION_STATES.has(session.state);
    const sessionError = session.error ? loginErrorLabel(session.error) : "";
    if (active) {
      sessionStorage.setItem(SESSION_KEY, session.id);
    } else {
      sessionStorage.removeItem(SESSION_KEY);
    }

    $("loginSessionState").textContent = sessionStateLabel(session.state);
    $("loginSessionExpiry").textContent = formatExpiry(session.expires_at);
    const dot = $("loginSessionDot");
    dot.className = "session-dot";
    if (session.state === "synced" || session.state === "interactive") dot.classList.add("is-ok");
    if (session.state === "closed" || session.state === "expired" || session.state === "sync_failed") dot.classList.add("is-danger");

    $("loginTerminateBtn").hidden = !active;
    if (cookieStatus) renderManagedSummary(cookieStatus);

    switch (session.state) {
      case "starting":
        showOverlay("正在启动服务浏览器", "首次启动通常需要几秒钟", true, "");
        break;
      case "interactive":
        if (!hasFrame) showOverlay("正在连接登录画面", "连接后可直接使用鼠标和键盘", true, "");
        break;
      case "verifying":
      case "authenticated":
        showOverlay("正在校验 YouTube 登录", "浏览器会导出登录 Cookie 并更新稳定文件", true, "");
        break;
      case "not_logged_in":
        if (hasFrame) hideOverlay();
        showLoginError(sessionError || loginErrorLabel("not_logged_in"));
        break;
      case "sync_failed":
        if (hasFrame) hideOverlay();
        showLoginError(sessionError || loginErrorLabel("sync_failed"));
        break;
      case "synced":
        closeLoginSocket();
        clearFrame();
        showOverlay(
          "YouTube 登录已保存",
          "已同步 " + String(Number(session.cookie_count) || 0) + " 个 Cookie，可继续用于搜索和下载",
          false,
          "is-success"
        );
        showLoginOK("登录档案已保存，现在可继续使用搜索与下载。");
        refreshStatus().catch(() => {});
        break;
      case "expired":
        closeLoginSocket();
        clearFrame();
        showOverlay("登录会话已过期", "请重新开始一个登录会话", false, "is-error");
        showLoginError(loginErrorLabel("session_expired"));
        break;
      case "closed":
        closeLoginSocket();
        clearFrame();
        if (sessionError) {
          showOverlay("服务浏览器已结束", sessionError, false, "is-error");
          showLoginError(sessionError);
        } else {
          showOverlay("登录会话已结束", "可以随时重新开始", false, "");
        }
        break;
    }
    updateControlAvailability();
  }

  function closeLoginSocket() {
    socketReady = false;
    if (loginSocket) {
      const socket = loginSocket;
      loginSocket = null;
      socket.onopen = null;
      socket.onmessage = null;
      socket.onerror = null;
      socket.onclose = null;
      try { socket.close(); } catch (_) {}
    }
    updateControlAvailability();
  }

  function websocketURL(path) {
    const scheme = location.protocol === "https:" ? "wss:" : "ws:";
    return scheme + "//" + location.host + path;
  }

  function connectLoginChannel() {
    if (!loginSession || !loginSession.channel_path || !CONTROL_SESSION_STATES.has(loginSession.state)) return;
    if (loginSocket && (loginSocket.readyState === WebSocket.OPEN || loginSocket.readyState === WebSocket.CONNECTING)) return;

    closeLoginSocket();
    showOverlay("正在连接登录画面", "正在建立受保护的实时通道", true, "");
    const socket = new WebSocket(websocketURL(loginSession.channel_path));
    socket.binaryType = "blob";
    loginSocket = socket;

    socket.onopen = () => {
      if (loginSocket !== socket) return;
      socketReady = true;
      updateControlAvailability();
      requestSurfaceViewport();
      if (!hasFrame) showOverlay("正在接收登录画面", "服务浏览器已经连接", true, "");
    };
    socket.onmessage = (event) => {
      if (loginSocket !== socket) return;
      if (typeof event.data !== "string") {
        displayFrame(event.data instanceof Blob ? event.data : new Blob([event.data], { type: "image/jpeg" }));
        return;
      }
      let message = null;
      try { message = JSON.parse(event.data); } catch (_) { return; }
      if (message.type === "status" && message.session) {
        renderSession(message.session);
        return;
      }
      if (message.type === "viewport") {
        updateViewport(message);
        return;
      }
      if (message.type === "error") {
        showLoginError(loginErrorLabel(message.code));
      }
    };
    socket.onerror = () => {
      if (loginSocket === socket) showLoginError("登录画面连接出现波动，请尝试重连。 ");
    };
    socket.onclose = () => {
      if (loginSocket !== socket) return;
      loginSocket = null;
      socketReady = false;
      updateControlAvailability();
      if (loginSession && CONTROL_SESSION_STATES.has(loginSession.state)) {
        showLoginError("登录画面连接已断开；服务浏览器仍会保留到会话到期。 ");
      }
    };
  }

  function scheduleSessionPoll(delay) {
    clearTimeout(sessionPollTimer);
    if (!loginSession || !ACTIVE_SESSION_STATES.has(loginSession.state)) return;
    sessionPollTimer = window.setTimeout(pollSession, delay || 1200);
  }

  async function pollSession() {
    if (!loginSession || !loginSession.id) return;
    const id = loginSession.id;
    try {
      const { r, j } = await api("/api/admin/youtube-login/sessions/" + encodeURIComponent(id), { cache: "no-store" });
      if (r.status === 401) {
        location.replace("./login.html");
        return;
      }
      if (r.status === 404 || r.status === 410) {
        sessionStorage.removeItem(SESSION_KEY);
        closeLoginSocket();
        const previous = loginSession;
        if (previous) {
          renderSession({
            ...previous,
            state: r.status === 410 ? "expired" : "closed",
            error: r.status === 410 ? "session_expired" : "session_missing",
          });
        } else {
          showLoginError(loginErrorLabel(r.status === 410 ? "session_expired" : "session_missing"));
        }
        return;
      }
      if (!r.ok || !j.session) throw new Error(apiError(j, "登录会话状态获取失败"));
      renderSession(j.session);
      if (CONTROL_SESSION_STATES.has(j.session.state)) connectLoginChannel();
      scheduleSessionPoll(socketReady ? 4000 : 900);
    } catch (error) {
      showLoginError(error.message || String(error));
      scheduleSessionPoll(2500);
    }
  }

  async function recoverStoredSession() {
    const id = sessionStorage.getItem(SESSION_KEY);
    if (!id) return;
    try {
      const { r, j } = await api("/api/admin/youtube-login/sessions/" + encodeURIComponent(id), { cache: "no-store" });
      if (!r.ok || !j.session) {
        sessionStorage.removeItem(SESSION_KEY);
        return;
      }
      renderSession(j.session);
      if (CONTROL_SESSION_STATES.has(j.session.state)) connectLoginChannel();
      scheduleSessionPoll(1000);
    } catch (_) {
      sessionStorage.removeItem(SESSION_KEY);
    }
  }

  async function startLogin() {
    clearLoginMessages();
    $("disconnectConfirm").hidden = true;
    const button = $("loginStartBtn");
    button.disabled = true;
    button.textContent = "正在创建会话";
    try {
      const { r, j } = await api("/api/admin/youtube-login/sessions", { method: "POST" });
      if (r.status === 401) {
        location.replace("./login.html");
        return;
      }
      if (!r.ok || !j.session) {
        const code = j && j.error && j.error.code;
        throw new Error(code ? loginErrorLabel(code) : apiError(j, "登录会话创建失败"));
      }
      clearFrame();
      renderSession(j.session);
      scheduleSessionPoll(350);
    } catch (error) {
      showLoginError(error.message || String(error));
      if (cookieStatus) renderManagedSummary(cookieStatus);
    }
  }

  async function verifyLogin() {
    if (!loginSession || !CONTROL_SESSION_STATES.has(loginSession.state)) return;
    clearLoginMessages();
    if (socketReady && loginSocket && loginSocket.readyState === WebSocket.OPEN) {
      renderSession({ ...loginSession, state: "verifying", error: "" });
      if (sendLoginMessage({ type: "verify" })) return;
    }
    try {
      renderSession({ ...loginSession, state: "verifying", error: "" });
      const path = "/api/admin/youtube-login/sessions/" + encodeURIComponent(loginSession.id) + "/verify";
      const { r, j } = await api(path, { method: "POST" });
      if (!r.ok || !j.session) {
        const code = j && j.error && j.error.code;
        throw new Error(code ? loginErrorLabel(code) : apiError(j, "登录校验未完成"));
      }
      renderSession(j.session);
    } catch (error) {
      showLoginError(error.message || String(error));
      scheduleSessionPoll(500);
    }
  }

  async function terminateLogin() {
    if (!loginSession || !loginSession.id) return;
    clearLoginMessages();
    $("loginTerminateBtn").disabled = true;
    try {
      const path = "/api/admin/youtube-login/sessions/" + encodeURIComponent(loginSession.id);
      const { r, j } = await api(path, { method: "DELETE" });
      if (!r.ok || !j.session) throw new Error(apiError(j, "登录会话结束失败"));
      renderSession(j.session);
      showLoginOK("登录会话已结束，持久化档案保持原状。 ");
    } catch (error) {
      showLoginError(error.message || String(error));
    } finally {
      $("loginTerminateBtn").disabled = false;
    }
  }

  async function disconnectManagedLogin() {
    $("disconnectConfirmBtn").disabled = true;
    clearLoginMessages();
    try {
      const { r, j } = await api("/api/admin/youtube-login/disconnect", { method: "POST" });
      if (!r.ok || j.ok === false) throw new Error(apiError(j, "断开登录未完成"));
      closeLoginSocket();
      clearFrame();
      loginSession = null;
      sessionStorage.removeItem(SESSION_KEY);
      $("loginWorkspace").hidden = true;
      $("disconnectConfirm").hidden = true;
      showLoginOK("服务浏览器中的 YouTube 登录已清除。 ");
      await refreshStatus();
    } catch (error) {
      showLoginError(error.message || String(error));
    } finally {
      $("disconnectConfirmBtn").disabled = false;
    }
  }

  function sendLoginMessage(message) {
    if (!socketReady || !loginSocket || loginSocket.readyState !== WebSocket.OPEN) return false;
    try {
      loginSocket.send(JSON.stringify(message));
      return true;
    } catch (_) {
      return false;
    }
  }

  function pointerPosition(event) {
    const rect = $("loginSurface").getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0) return null;
    return {
      x: clamp((event.clientX - rect.left) / rect.width, 0, 1),
      y: clamp((event.clientY - rect.top) / rect.height, 0, 1),
    };
  }

  function pointerButton(button) {
    return ["left", "middle", "right", "back", "forward"][button] || "none";
  }

  function sendPointer(type, event) {
    const point = pointerPosition(event);
    if (!point) return;
    sendLoginMessage({
      type: "mouse",
      event_type: type,
      x: point.x,
      y: point.y,
      button: type === "mouseMoved" || type === "mouseWheel" ? "none" : pointerButton(event.button),
      buttons: Number(event.buttons) || 0,
      click_count: type === "mousePressed" || type === "mouseReleased" ? clamp(Number(event.detail) || 1, 1, 3) : 0,
      delta_x: type === "mouseWheel" ? Number(event.deltaX) || 0 : 0,
      delta_y: type === "mouseWheel" ? Number(event.deltaY) || 0 : 0,
      modifiers: eventModifiers(event),
    });
  }

  function eventModifiers(event) {
    return (event.altKey ? 1 : 0) |
      (event.ctrlKey ? 2 : 0) |
      (event.metaKey ? 4 : 0) |
      (event.shiftKey ? 8 : 0);
  }

  function sendKey(eventType, event) {
    if (!loginSession || !CONTROL_SESSION_STATES.has(loginSession.state)) return;
    const printable = eventType === "keyDown" && event.key && event.key.length === 1 && !event.ctrlKey && !event.metaKey && !event.altKey;
    if (sendLoginMessage({
      type: "key",
      event_type: eventType,
      key: event.key || "",
      code: event.code || "",
      text: printable ? event.key : "",
      windows_key_code: Number(event.keyCode) || 0,
      modifiers: eventModifiers(event),
    })) {
      event.preventDefault();
    }
  }

  function requestSurfaceViewport() {
    clearTimeout(resizeTimer);
    resizeTimer = window.setTimeout(() => {
      if (!socketReady) return;
      const surface = $("loginSurface");
      const cssWidth = Math.max(1, surface.clientWidth);
      const width = clamp(Math.round(cssWidth * Math.min(window.devicePixelRatio || 1, 1.5)), 480, 1440);
      const height = clamp(Math.round(width / 1.6), 300, 1000);
      if (Math.abs(width - lastRequestedViewport.width) < 16 && Math.abs(height - lastRequestedViewport.height) < 16) return;
      lastRequestedViewport = { width, height };
      sendLoginMessage({ type: "resize", width, height });
    }, 180);
  }

  function bindLoginSurface() {
    const surface = $("loginSurface");
    surface.addEventListener("contextmenu", (event) => event.preventDefault());
    surface.addEventListener("pointerdown", (event) => {
      if (!socketReady) return;
      event.preventDefault();
      surface.focus({ preventScroll: true });
      try { surface.setPointerCapture(event.pointerId); } catch (_) {}
      sendPointer("mouseMoved", event);
      sendPointer("mousePressed", event);
    });
    surface.addEventListener("pointerup", (event) => {
      if (!socketReady) return;
      event.preventDefault();
      sendPointer("mouseReleased", event);
      try { surface.releasePointerCapture(event.pointerId); } catch (_) {}
    });
    surface.addEventListener("pointermove", (event) => {
      if (!socketReady) return;
      queuedPointerMove = event;
      if (pointerAnimationFrame) return;
      pointerAnimationFrame = requestAnimationFrame(() => {
        pointerAnimationFrame = 0;
        const queued = queuedPointerMove;
        queuedPointerMove = null;
        if (queued) sendPointer("mouseMoved", queued);
      });
    });
    surface.addEventListener("wheel", (event) => {
      if (!socketReady) return;
      event.preventDefault();
      sendPointer("mouseWheel", event);
    }, { passive: false });
    surface.addEventListener("keydown", (event) => {
      if (event.isComposing) return;
      sendKey("keyDown", event);
    });
    surface.addEventListener("keyup", (event) => {
      if (event.isComposing) return;
      sendKey("keyUp", event);
    });

    if ("ResizeObserver" in window) {
      new ResizeObserver(requestSurfaceViewport).observe(surface);
    } else {
      window.addEventListener("resize", requestSurfaceViewport);
    }
  }

  function bindTextRelay() {
    $("loginTextToggle").addEventListener("click", () => {
      const input = $("loginTextInput");
      const show = input.type === "password";
      input.type = show ? "text" : "password";
      $("loginTextToggle").textContent = show ? "隐藏" : "显示";
    });
    $("loginTextForm").addEventListener("submit", (event) => {
      event.preventDefault();
      const input = $("loginTextInput");
      const text = input.value;
      if (!text) return;
      if (!sendLoginMessage({ type: "text", text })) {
        showLoginError("登录画面尚未连接。 ");
        return;
      }
      input.value = "";
      input.type = "password";
      $("loginTextToggle").textContent = "显示";
      $("loginSurface").focus({ preventScroll: true });
    });
  }

  function setFile(file) {
    selectedFile = file || null;
    $("fileName").textContent = selectedFile ? selectedFile.name : "未选择文件";
    $("uploadBtn").disabled = !selectedFile;
    $("uploadErr").textContent = "";
    $("uploadOk").textContent = "";
    if (!selectedFile) $("fileInput").value = "";
  }

  async function upload() {
    if (!selectedFile) return;
    $("uploadErr").textContent = "";
    $("uploadOk").textContent = "";
    $("uploadBtn").disabled = true;
    try {
      const fd = new FormData();
      fd.append("file", selectedFile, selectedFile.name);
      const { r, j } = await api("/api/admin/cookies/upload", { method: "POST", body: fd });
      if (r.status === 401) {
        location.replace("./login.html");
        return;
      }
      if (!r.ok || j.ok === false) throw new Error(apiError(j, "上传失败"));
      $("uploadOk").textContent = j.message || "上传成功";
      setFile(null);
      await refreshStatus();
    } catch (error) {
      $("uploadErr").textContent = error.message || String(error);
      $("uploadBtn").disabled = !selectedFile;
    }
  }

  function bindUpload() {
    const dropzone = $("dropzone");
    const input = $("fileInput");
    dropzone.addEventListener("click", () => input.click());
    dropzone.addEventListener("keydown", (event) => {
      if (event.key === "Enter" || event.key === " ") {
        event.preventDefault();
        input.click();
      }
    });
    input.addEventListener("change", () => setFile(input.files && input.files[0]));
    ["dragenter", "dragover"].forEach((name) => {
      dropzone.addEventListener(name, (event) => {
        event.preventDefault();
        dropzone.classList.add("dragover");
      });
    });
    ["dragleave", "drop"].forEach((name) => {
      dropzone.addEventListener(name, (event) => {
        event.preventDefault();
        dropzone.classList.remove("dragover");
      });
    });
    dropzone.addEventListener("drop", (event) => {
      const file = event.dataTransfer && event.dataTransfer.files && event.dataTransfer.files[0];
      if (file) setFile(file);
    });
    $("uploadBtn").addEventListener("click", upload);
  }

  async function logout() {
    if (loginSession && ACTIVE_SESSION_STATES.has(loginSession.state)) {
      try {
        await api("/api/admin/youtube-login/sessions/" + encodeURIComponent(loginSession.id), { method: "DELETE" });
      } catch (_) {}
    }
    closeLoginSocket();
    sessionStorage.removeItem(SESSION_KEY);
    await api("/api/admin/logout", { method: "POST" });
    location.replace("./login.html");
  }

  function bindActions() {
    $("loginStartBtn").addEventListener("click", startLogin);
    $("loginVerifyBtn").addEventListener("click", verifyLogin);
    $("loginTerminateBtn").addEventListener("click", terminateLogin);
    $("loginReconnectBtn").addEventListener("click", connectLoginChannel);
    $("managedDisconnectBtn").addEventListener("click", () => {
      clearLoginMessages();
      $("disconnectConfirm").hidden = false;
    });
    $("disconnectCancelBtn").addEventListener("click", () => { $("disconnectConfirm").hidden = true; });
    $("disconnectConfirmBtn").addEventListener("click", disconnectManagedLogin);
    $("refreshStatusBtn").addEventListener("click", () => refreshStatus().catch((error) => showLoginError(error.message || String(error))));
    $("logoutBtn").addEventListener("click", logout);
    bindLoginSurface();
    bindTextRelay();
    bindUpload();
  }

  async function main() {
    bindActions();
    const auth = await ensureAuth();
    if (!auth.ok) {
      if (auth.reason === "disabled") {
        showLoginError("管理端尚未启用：请设置环境变量 ADMIN_PASSWORD。 ");
        $("stPresent").textContent = "未启用";
      }
      return;
    }
    await refreshStatus();
    await recoverStoredSession();
  }

  window.addEventListener("pagehide", () => {
    clearTimeout(sessionPollTimer);
    clearTimeout(resizeTimer);
    if (pointerAnimationFrame) cancelAnimationFrame(pointerAnimationFrame);
    closeLoginSocket();
    clearFrame();
  });

  main().catch((error) => {
    const target = $("loginErr") || $("uploadErr");
    if (target) target.textContent = error.message || String(error);
  });
})();
