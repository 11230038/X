#!/usr/bin/env bash
# Start docling, the backend and the frontend together (Git Bash).
#
#   bash dev.sh
#
# docling is reused when it is already healthy, otherwise it is started with the
# four environment variables this machine needs (see the notes in Agent.md) and
# the script waits for /health before starting the backend. The backend and the
# frontend already running are reused as well, so this script never starts a
# second copy on the same port. All three run in the background with their
# output under data/; Ctrl+C stops the ones this script started — docling
# included — and leaves alone whatever was already running before it.
#
# Overridable:
#   DOCLING_EXE              docling-serve executable
#   DOCLING_URL              base URL, default http://127.0.0.1:5001
#   DOCLING_READY_TIMEOUT    seconds to wait for /health, default 180
#   BACKEND_URL              health URL, default http://127.0.0.1:8080
#   FRONTEND_URL             dev server URL, default http://localhost:5173
#   BACKEND_READY_TIMEOUT    seconds to wait for /healthz, default 180
#   FRONTEND_READY_TIMEOUT   seconds to wait for the dev server, default 120
set -uo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DOCLING_EXE="${DOCLING_EXE:-C:/test/docling-serve/venv/Scripts/docling-serve.exe}"
DOCLING_URL="${DOCLING_URL:-http://127.0.0.1:5001}"
DOCLING_READY_TIMEOUT="${DOCLING_READY_TIMEOUT:-180}"
BACKEND_URL="${BACKEND_URL:-http://127.0.0.1:8080}"
# Vite 默认只监听 localhost（这台机器上是 ::1），用 127.0.0.1 探测会连不上。
FRONTEND_URL="${FRONTEND_URL:-http://localhost:5173}"
BACKEND_READY_TIMEOUT="${BACKEND_READY_TIMEOUT:-180}"
FRONTEND_READY_TIMEOUT="${FRONTEND_READY_TIMEOUT:-120}"
DOCLING_LOG="$ROOT/data/docling.log"
BACKEND_LOG="$ROOT/data/backend.log"
FRONTEND_LOG="$ROOT/data/frontend.log"

hostport="${DOCLING_URL#*://}"
hostport="${hostport%%/*}"
port="${hostport##*:}"
if [ "$port" = "$hostport" ]; then
  port=5001
fi

backend_pid=""
frontend_pid=""
docling_pid=""
cleaned=0

stop_tree() {
  local pid="$1" winpid
  [ -z "$pid" ] && return
  winpid="$(cat "/proc/$pid/winpid" 2>/dev/null || true)"
  if [ -n "$winpid" ]; then
    taskkill //PID "$winpid" //T //F >/dev/null 2>&1 || true
  fi
  kill "$pid" 2>/dev/null || true
}

cleanup() {
  [ "$cleaned" = 1 ] && return
  cleaned=1
  stop_tree "$frontend_pid"
  stop_tree "$backend_pid"
  stop_tree "$docling_pid"
}

trap cleanup EXIT
trap 'exit 130' INT TERM

healthy() {
  curl -fsS -o /dev/null --max-time 2 "$1" >/dev/null 2>&1
}

# wait_http <url> <timeout> <pid> <log> <label>
wait_http() {
  local url="$1" timeout="$2" pid="$3" log="$4" label="$5" waited=0
  while ! healthy "$url"; do
    if ! kill -0 "$pid" 2>/dev/null; then
      echo "[$label] 进程已退出。日志尾部：" >&2
      tail -n 20 "$log" >&2
      return 1
    fi
    if [ "$waited" -ge "$timeout" ]; then
      echo "[$label] ${timeout} 秒内没有就绪。日志尾部：" >&2
      tail -n 20 "$log" >&2
      return 1
    fi
    sleep 2
    waited=$((waited + 2))
  done
}

mkdir -p "$ROOT/data"

# --- docling ---------------------------------------------------------------
if healthy "$DOCLING_URL/health"; then
  echo "[docling] 已在运行：$DOCLING_URL"
else
  if [ ! -f "$DOCLING_EXE" ]; then
    echo "[docling] 找不到 $DOCLING_EXE" >&2
    echo "[docling] 用 DOCLING_EXE=/path/to/docling-serve.exe 指定安装位置" >&2
    exit 1
  fi
  echo "[docling] 启动中，日志：$DOCLING_LOG"
  (
    cd "$ROOT" || exit 1
    export PYTHONUTF8=1
    export PYTHONIOENCODING=utf-8
    export HF_ENDPOINT="${HF_ENDPOINT:-https://hf-mirror.com}"
    export HF_HUB_DISABLE_XET="${HF_HUB_DISABLE_XET:-1}"
    exec "$DOCLING_EXE" run --host 127.0.0.1 --port "$port"
  ) >>"$DOCLING_LOG" 2>&1 &
  docling_pid=$!

  echo "[docling] 等待 ${DOCLING_URL}/health（最多 ${DOCLING_READY_TIMEOUT} 秒）…"
  waited=0
  until healthy "$DOCLING_URL/health"; do
    if [ "$waited" -ge "$DOCLING_READY_TIMEOUT" ]; then
      echo "[docling] ${DOCLING_READY_TIMEOUT} 秒内没有就绪，后端未启动。最后 20 行日志：" >&2
      tail -n 20 "$DOCLING_LOG" >&2
      exit 1
    fi
    sleep 3
    waited=$((waited + 3))
  done
  echo "[docling] 已就绪（用时约 ${waited} 秒）"
fi

if grep -qE '^PARSER_DEFAULT_ENGINE=docling\r?$' "$ROOT/backend/.env" 2>/dev/null; then
  echo "[docling] backend/.env 的解析引擎是 docling"
else
  echo "[提示] backend/.env 的 PARSER_DEFAULT_ENGINE 不是 docling，后端暂时不会调用它"
fi

# --- backend ---------------------------------------------------------------
if healthy "$BACKEND_URL/healthz"; then
  echo "[backend] 已在运行：$BACKEND_URL"
else
  echo "[backend] 启动 go run .，日志：$BACKEND_LOG"
  (
    cd "$ROOT/backend" || exit 1
    exec go run .
  ) >>"$BACKEND_LOG" 2>&1 &
  backend_pid=$!
fi

# --- frontend --------------------------------------------------------------
if healthy "$FRONTEND_URL/"; then
  echo "[frontend] 已在运行：$FRONTEND_URL"
else
  echo "[frontend] 启动 npm run dev，日志：$FRONTEND_LOG"
  (
    cd "$ROOT/frontend" || exit 1
    exec npm run dev -- --strictPort
  ) >>"$FRONTEND_LOG" 2>&1 &
  frontend_pid=$!
fi

if [ -n "$backend_pid" ]; then
  echo "[backend] 等待 ${BACKEND_URL}/healthz（最多 ${BACKEND_READY_TIMEOUT} 秒）…"
  wait_http "$BACKEND_URL/healthz" "$BACKEND_READY_TIMEOUT" "$backend_pid" "$BACKEND_LOG" backend || exit 1
fi

if [ -n "$frontend_pid" ]; then
  echo "[frontend] 等待 $FRONTEND_URL（最多 ${FRONTEND_READY_TIMEOUT} 秒）…"
  wait_http "$FRONTEND_URL/" "$FRONTEND_READY_TIMEOUT" "$frontend_pid" "$FRONTEND_LOG" frontend || exit 1
fi

echo
echo "docling   $DOCLING_URL"
echo "backend   $BACKEND_URL（日志 $BACKEND_LOG）"
echo "frontend  $FRONTEND_URL（日志 $FRONTEND_LOG）"
echo "Ctrl+C 停止本次启动的后端、前端与 docling；启动前已在运行的服务不受影响。"

# Stay in the foreground so Ctrl+C reaches this script. Stop the other side as
# soon as one of them exits on its own. An empty pid means that service was
# already running and was reused, so it never counts as exited.
alive_or_reused() {
  [ -z "$1" ] && return 0
  kill -0 "$1" 2>/dev/null
}

while alive_or_reused "$backend_pid" && alive_or_reused "$frontend_pid"; do
  sleep 2
done

if [ -n "$backend_pid" ] && ! kill -0 "$backend_pid" 2>/dev/null; then
  echo "[backend] 已退出，日志尾部：" >&2
  tail -n 20 "$BACKEND_LOG" >&2
else
  echo "[frontend] 已退出，日志尾部：" >&2
  tail -n 20 "$FRONTEND_LOG" >&2
fi
exit 1
