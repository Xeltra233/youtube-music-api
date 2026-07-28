(function () {
  const $ = (id) => document.getElementById(id);

  async function api(path, opts = {}) {
    const r = await fetch(path, {
      credentials: "same-origin",
      ...opts,
    });
    const j = await r.json().catch(() => ({}));
    return { r, j };
  }

  async function ensureAuth() {
    const { r, j } = await api("/api/admin/check-auth");
    if (!r.ok) {
      // network/server issue: stay and show error via refreshStatus
      return { ok: false, reason: "error" };
    }
    if (j.enabled === false) {
      return { ok: false, reason: "disabled" };
    }
    if (!j.authenticated) {
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

  function renderStatus(st) {
    $("stPresent").textContent = st.present ? "已就绪" : "未上传";
    $("stPresent").style.color = st.present ? "var(--ok)" : "var(--muted)";
    $("stFile").textContent = st.active_file || "-";
    $("stSize").textContent = st.present ? fmtBytes(st.size_bytes) : "-";
    $("stMod").textContent = st.modified_at || "-";
    if (st.keepalive) {
      const sec = Number(st.keepalive_interval) || 0;
      const hrs = sec ? (sec / 3600).toFixed(sec % 3600 === 0 ? 0 : 1) : "?";
      $("stKeep").textContent = "开启 · 每 " + hrs + " 小时";
    } else {
      $("stKeep").textContent = "关闭";
    }
    $("stCount").textContent = String(st.dropin_files ?? 0);
  }

  async function refreshStatus() {
    const { r, j } = await api("/api/admin/cookies/status");
    if (r.status === 401) {
      $("stPresent").textContent = "未登录";
      $("stPresent").style.color = "var(--danger)";
      $("stFile").textContent = "-";
      $("stSize").textContent = "-";
      $("stMod").textContent = "-";
      $("stKeep").textContent = "-";
      $("stCount").textContent = "-";
      $("uploadErr").textContent = "未登录或会话过期，请先登录";
      $("uploadBtn").disabled = true;
      return;
    }
    if (!r.ok) throw new Error((j.error && j.error.message) || "状态获取失败");
    $("uploadErr").textContent = "";
    renderStatus(j);
  }

  let selectedFile = null;

  function setFile(file) {
    selectedFile = file || null;
    $("fileName").textContent = selectedFile ? selectedFile.name : "未选择文件";
    $("uploadBtn").disabled = !selectedFile;
    $("uploadErr").textContent = "";
    $("uploadOk").textContent = "";
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
      if (!r.ok || j.ok === false) {
        throw new Error((j.error && j.error.message) || "上传失败");
      }
      $("uploadOk").textContent = j.message || "上传成功";
      setFile(null);
      await refreshStatus();
    } catch (e) {
      $("uploadErr").textContent = e.message || String(e);
      $("uploadBtn").disabled = !selectedFile;
    }
  }

  async function logout() {
    await api("/api/admin/logout", { method: "POST" });
    location.replace("./login.html");
  }

  async function main() {
    const auth = await ensureAuth();
    if (!auth.ok) {
      if (auth.reason === "disabled") {
        $("uploadErr").textContent = "管理端未启用：请设置环境变量 ADMIN_PASSWORD";
        $("stPresent").textContent = "未启用";
      } else if (auth.reason === "unauthenticated") {
        // Keep upload page visible for layout; offer login CTA via error text.
        $("uploadErr").innerHTML = '未登录。请先打开 <a href=\"./login.html\" style=\"color:var(--accent)\">登录页</a>';
        $("stPresent").textContent = "未登录";
        $("stPresent").style.color = "var(--danger)";
      } else {
        $("uploadErr").textContent = "无法检查登录状态";
      }
    } else {
      await refreshStatus();
    }

    const dz = $("dropzone");
    const input = $("fileInput");
    dz.addEventListener("click", () => input.click());
    dz.addEventListener("keydown", (e) => {
      if (e.key === "Enter" || e.key === " ") {
        e.preventDefault();
        input.click();
      }
    });
    input.addEventListener("change", () => setFile(input.files && input.files[0]));

    ["dragenter", "dragover"].forEach((ev) => {
      dz.addEventListener(ev, (e) => {
        e.preventDefault();
        dz.classList.add("dragover");
      });
    });
    ["dragleave", "drop"].forEach((ev) => {
      dz.addEventListener(ev, (e) => {
        e.preventDefault();
        dz.classList.remove("dragover");
      });
    });
    dz.addEventListener("drop", (e) => {
      const f = e.dataTransfer && e.dataTransfer.files && e.dataTransfer.files[0];
      if (f) setFile(f);
    });

    $("uploadBtn").addEventListener("click", upload);
    $("logoutBtn").addEventListener("click", logout);
  }

  main().catch((e) => {
    const err = $("uploadErr");
    if (err) err.textContent = e.message || String(e);
  });
})();
