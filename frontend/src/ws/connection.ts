/**
 * 一条 WebSocket 连接的生命周期。
 *
 * 这个文件是**全应用唯一碰 `WebSocket` 构造器的地方**。上面那一层
 * （`socket.ts`）只认 `Link` 这个把手：不知道 `readyState`，不知道关闭码，也
 * 就没有机会再 `new WebSocket` 第二次。断了之后把手还在（`isOpen()` 会说
 * false），所以"还能不能发"这个问题只有一个问法。
 *
 * 它单独成文件的主要理由是**把"握手之前"与"握手之后"的失败分开**。浏览器在
 * 这两种情况下给的东西几乎一样——都是一次 `onerror` 加一个 1006——只有"建起
 * 来过没有"这个标志能把它们分开（《设计说明书》§4.2 要求这两者的出口不同：
 * 一个说"连不上"，一个说"服务端可能在让你等几秒"）。那个标志活在这里，是它
 * 唯一该在的地方。
 */

export interface LinkHandlers {
  /** 连上了。一次连接至多一次。 */
  onOpen(): void
  /** 收到一帧，原文，没有解析过。解析归上层——连接层对帧的内容一无所知。 */
  onFrame(raw: string): void
  /**
   * **根本没建起来。** `onOpen` 与 `onRefused` 恰好发生一个，都在 `settled`
   * 结掉之前。
   */
  onRefused(): void
  /**
   * 连上了之后断了。只可能跟在 `onOpen` 后面。
   *
   * 关闭码照实传出来：1001 是服务端关机，1008 是慢消费者，1006 是浏览器自己
   * 报的（没有关闭帧）。重连策略对所有码相同，但界面要说得出是哪一种。
   */
  onLost(closeCode: number, reason: string): void
}

export interface Link {
  isOpen(): boolean
  /** 没连上、已经关了、或者正在关的时候是**空操作**——不排队，也不抛。 */
  send(raw: string): void
  close(): void
}

export interface PendingLink {
  /**
   * **同步**就拿到手的把手。连接还在建的时候它也在——所以调用方不必等
   * `settled` 才有东西可发（发不出去就是空操作）、可关。少了这一点，组件卸载
   * 时正好卡在握手上就会留下一条关不掉的连接。
   */
  link: Link
  /** 连上了、或者确定没连上。两种情况都会结，不会永远挂着。 */
  settled: Promise<void>
}

/**
 * 票进查询串。**必须同源**：后端不开 CORS，开发期由 Vite 代理转给后端，生产
 * 部署时前端与 API 同一个源（《前端设计注意事项》§1.2）。
 */
function socketURL(ticket: string): string {
  const url = new URL('/api/v1/ws', window.location.origin)
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  url.searchParams.set('ticket', ticket)
  return url.toString()
}

export function openLink(ticket: string, handlers: LinkHandlers): PendingLink {
  // 握手之前与之后是两种失败，所以要记着"建起来没有"：`onerror` 在两种情况都
  // 会来，只有这个标志能把它们分开。
  let opened = false
  let refused = false
  let settled = false
  let resolveSettled: () => void = () => {}

  const settledPromise = new Promise<void>((resolve) => {
    resolveSettled = resolve
  })

  function settle(): void {
    if (settled) {
      return
    }
    settled = true
    resolveSettled()
  }

  /** 两个出口（`onerror` 与没连上的 `onclose`）都要能结掉，但**只报一次**。 */
  function refuse(): void {
    if (!refused) {
      refused = true
      handlers.onRefused()
    }
    settle()
  }

  const socket = new WebSocket(socketURL(ticket))

  socket.onopen = () => {
    opened = true
    handlers.onOpen()
    settle()
  }

  socket.onmessage = (message) => {
    handlers.onFrame(message.data as string)
  }

  socket.onerror = () => {
    // 浏览器**只说"出错了"，不说是第几种**：WebSocket API 不暴露握手响应的状
    // 态码，所以一个 429（服务端让等几秒）和一次断网在这里长得一模一样，都是
    // 随后的 1006。这是浏览器平台的限制，不是这里写漏了。
    //
    // 因此只报"没建起来"，**不猜原因**。要分辨到底是不是被限流，只能另外发一
    // 个普通 HTTP 请求去观察——stage 0 不做这件事。
    if (!opened) {
      refuse()
    }
  }

  socket.onclose = (event) => {
    if (!opened) {
      // 没连上就关了。`onerror` 通常先到，但只靠它不可靠——这里也结一次，否则
      // 调用方的 `await` 会永远挂着。
      refuse()
      return
    }
    handlers.onLost(event.code, event.reason)
  }

  return {
    link: {
      isOpen: () => socket.readyState === WebSocket.OPEN,
      send: (raw) => {
        if (socket.readyState === WebSocket.OPEN) {
          socket.send(raw)
        }
      },
      close: () => {
        socket.close(1000, '')
      },
    },
    settled: settledPromise,
  }
}

/**
 * 握手失败之后，用一次普通 HTTP 请求补看服务端到底说了什么。
 *
 * 浏览器在握手被拒时只给 `onerror` + 1006，**看不到 429 与 `Retry-After`**。
 * 但同一个地址、同一张票走一次普通 `fetch`，限流中间件会在升级之前回 429，
 * 于是那个响应头就看得见了。只用来分辨「是不是被限流」，其他状态一律当作
 * 「没建起来」，不猜原因。
 *
 * 票可能已经随着失败的握手过期，所以读不到也不影响结论：调用方退回退避。
 */
export async function probeHandshakeRetry(ticket: string): Promise<number | null> {
  try {
    const response = await fetch(socketURL(ticket), {
      method: 'GET',
      headers: { Accept: 'text/plain' },
      credentials: 'same-origin',
    })
    // 不读 body：升级端点回什么正文都不影响这里。
    if (response.status !== 429) return null
    const seconds = Number.parseInt(response.headers.get('Retry-After') ?? '', 10)
    return Number.isFinite(seconds) && seconds > 0 ? seconds : null
  } catch {
    return null
  }
}
