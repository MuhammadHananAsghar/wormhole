import type { Env } from "./index";

interface PendingRequest {
  resolve: (response: Response) => void;
  reject: (error: Error) => void;
  timeout: ReturnType<typeof setTimeout>;
}

interface TunnelMessage {
  type: string;
  id?: string;
  [key: string]: unknown;
}

interface HttpResponseMessage {
  type: "http_response";
  id: string;
  status: number;
  headers: Record<string, string>;
  body: string; // base64 encoded
}

const REQUEST_TIMEOUT_MS = 30_000;

export class Tunnel implements DurableObject {
  private clientWs: WebSocket | null = null;
  private subdomain: string | null = null;
  private pendingRequests = new Map<string, PendingRequest>();
  private visitorWebSockets = new Map<string, WebSocket>(); // ws_id -> visitor WS
  private requestCounter = 0;

  constructor(
    private readonly state: DurableObjectState,
    private readonly env: Env,
  ) {}

  async fetch(request: Request): Promise<Response> {
    const url = new URL(request.url);

    // Handle WebSocket upgrade for tunnel registration
    if (url.pathname === "/_wormhole/register") {
      return this.handleRegister(request);
    }

    // Handle visitor WebSocket upgrade (passthrough)
    if (request.headers.get("Upgrade") === "websocket") {
      return this.handleVisitorWebSocket(request);
    }

    // Handle proxied HTTP requests
    return this.handleProxyRequest(request);
  }

  private handleRegister(request: Request): Response {
    const upgradeHeader = request.headers.get("Upgrade");
    if (upgradeHeader !== "websocket") {
      return new Response(JSON.stringify({ error: "Expected WebSocket upgrade" }), {
        status: 426,
        headers: { "Content-Type": "application/json" },
      });
    }

    const pair = new WebSocketPair();
    const [client, server] = Object.values(pair);

    this.state.acceptWebSocket(server);
    this.clientWs = server;

    return new Response(null, { status: 101, webSocket: client });
  }

  private getClientWs(): WebSocket | null {
    if (this.clientWs) return this.clientWs;
    // Recover WebSocket reference after hibernation
    const sockets = this.state.getWebSockets();
    if (sockets.length > 0) {
      this.clientWs = sockets[0];
      return this.clientWs;
    }
    return null;
  }

  private async handleProxyRequest(request: Request): Promise<Response> {
    const ws = this.getClientWs();
    if (!ws) {
      return new Response(
        JSON.stringify({ error: "Tunnel not connected. No client is currently connected to this tunnel." }),
        { status: 502, headers: { "Content-Type": "application/json" } },
      );
    }

    const requestId = `req_${++this.requestCounter}`;

    // Serialize the HTTP request to send over WebSocket
    const body = request.body ? await request.arrayBuffer() : null;
    const url = new URL(request.url);
    const headers = Object.fromEntries(request.headers);

    // Inject forwarding headers (Task 8)
    headers["x-forwarded-proto"] = url.protocol.replace(":", "");
    headers["x-forwarded-host"] = request.headers.get("host") || url.host;
    const clientIp = request.headers.get("cf-connecting-ip") || request.headers.get("x-real-ip") || "";
    if (clientIp) {
      headers["x-forwarded-for"] = clientIp;
    }

    const serialized: TunnelMessage = {
      type: "http_request",
      id: requestId,
      method: request.method,
      path: url.pathname + url.search,
      headers,
      body: body ? btoa(String.fromCharCode(...new Uint8Array(body))) : null,
    };

    // Create a promise that will be resolved when the response comes back
    const responsePromise = new Promise<Response>((resolve, reject) => {
      const timeout = setTimeout(() => {
        this.pendingRequests.delete(requestId);
        reject(new Error("Request timed out"));
      }, REQUEST_TIMEOUT_MS);

      this.pendingRequests.set(requestId, { resolve, reject, timeout });
    });

    try {
      ws.send(JSON.stringify(serialized));
    } catch {
      this.pendingRequests.delete(requestId);
      this.clientWs = null;
      return new Response(
        JSON.stringify({ error: "Tunnel not connected. No client is currently connected to this tunnel." }),
        { status: 502, headers: { "Content-Type": "application/json" } },
      );
    }

    try {
      return await responsePromise;
    } catch {
      return new Response(
        JSON.stringify({ error: "Request to tunnel timed out" }),
        { status: 504, headers: { "Content-Type": "application/json" } },
      );
    }
  }

  private handleVisitorWebSocket(request: Request): Response {
    const ws = this.getClientWs();
    if (!ws) {
      return new Response(
        JSON.stringify({ error: "Tunnel not connected." }),
        { status: 502, headers: { "Content-Type": "application/json" } },
      );
    }

    const pair = new WebSocketPair();
    const [visitorClient, visitorServer] = Object.values(pair);

    const wsId = `ws_${++this.requestCounter}`;
    const url = new URL(request.url);
    const headers = Object.fromEntries(request.headers);

    // Inject forwarding headers
    headers["x-forwarded-proto"] = url.protocol.replace(":", "");
    headers["x-forwarded-host"] = request.headers.get("host") || url.host;
    const clientIp = request.headers.get("cf-connecting-ip") || "";
    if (clientIp) headers["x-forwarded-for"] = clientIp;

    // Tell the Go client to open a WS connection to localhost
    try {
      ws.send(JSON.stringify({
        type: "ws_open",
        id: wsId,
        path: url.pathname + url.search,
        headers,
      }));
    } catch {
      return new Response(
        JSON.stringify({ error: "Tunnel not connected." }),
        { status: 502, headers: { "Content-Type": "application/json" } },
      );
    }

    visitorServer.accept();
    this.visitorWebSockets.set(wsId, visitorServer);

    // Forward visitor messages to Go client
    visitorServer.addEventListener("message", (event) => {
      const clientWs = this.getClientWs();
      if (!clientWs) return;
      const data = typeof event.data === "string" ? event.data : btoa(String.fromCharCode(...new Uint8Array(event.data as ArrayBuffer)));
      try {
        clientWs.send(JSON.stringify({
          type: "ws_frame",
          id: wsId,
          data: typeof event.data === "string" ? btoa(event.data) : data,
        }));
      } catch { /* client disconnected */ }
    });

    visitorServer.addEventListener("close", () => {
      this.visitorWebSockets.delete(wsId);
      const clientWs = this.getClientWs();
      if (clientWs) {
        try {
          clientWs.send(JSON.stringify({ type: "ws_close", id: wsId, code: 1000 }));
        } catch { /* ignore */ }
      }
    });

    return new Response(null, { status: 101, webSocket: visitorClient });
  }

  // Hibernatable WebSocket handlers
  async webSocketMessage(ws: WebSocket, message: string | ArrayBuffer): Promise<void> {
    if (typeof message !== "string") return;

    let parsed: TunnelMessage;
    try {
      parsed = JSON.parse(message);
    } catch {
      return;
    }

    switch (parsed.type) {
      case "register":
        await this.handleRegistration(ws, parsed);
        break;
      case "http_response":
        this.handleHttpResponse(parsed as unknown as HttpResponseMessage);
        break;
      case "ws_frame":
        this.handleWsFrame(parsed);
        break;
      case "ws_close":
        this.handleWsClose(parsed);
        break;
      case "pong":
        break;
    }
  }

  async webSocketClose(ws: WebSocket, code: number, reason: string, wasClean: boolean): Promise<void> {
    this.clientWs = null;

    // Recover subdomain from storage if lost during hibernation
    if (!this.subdomain) {
      this.subdomain = (await this.state.storage.get<string>("subdomain")) ?? null;
    }

    // Remove subdomain from D1 and storage
    if (this.subdomain) {
      await this.env.DB.prepare("DELETE FROM tunnels WHERE subdomain = ?")
        .bind(this.subdomain).run();
      await this.state.storage.delete("subdomain");
      this.subdomain = null;
    }

    // Reject all pending requests
    for (const [id, pending] of this.pendingRequests) {
      clearTimeout(pending.timeout);
      pending.reject(new Error("Client disconnected"));
      this.pendingRequests.delete(id);
    }
  }

  async webSocketError(ws: WebSocket, error: unknown): Promise<void> {
    if (ws === this.clientWs) {
      this.clientWs = null;
    }
  }

  private handleWsFrame(message: TunnelMessage): void {
    const id = message.id as string;
    const visitorWs = this.visitorWebSockets.get(id);
    if (!visitorWs) return;
    try {
      const decoded = atob(message.data as string);
      visitorWs.send(decoded);
    } catch { /* ignore */ }
  }

  private handleWsClose(message: TunnelMessage): void {
    const id = message.id as string;
    const visitorWs = this.visitorWebSockets.get(id);
    if (!visitorWs) return;
    this.visitorWebSockets.delete(id);
    try {
      visitorWs.close(1000);
    } catch { /* ignore */ }
  }

  private async validateToken(token: string): Promise<{ valid: boolean; username?: string; githubId?: string }> {
    if (!token) return { valid: false };
    try {
      const row = await this.env.DB.prepare(
        "SELECT github_id, username FROM users WHERE token = ?"
      ).bind(token).first<{ github_id: string; username: string }>();
      if (!row) return { valid: false };
      return { valid: true, username: row.username, githubId: row.github_id };
    } catch {
      return { valid: false };
    }
  }

  private async handleRegistration(ws: WebSocket, message: TunnelMessage): Promise<void> {
    const requested = typeof message.subdomain === "string" ? message.subdomain.toLowerCase().trim() : "";
    const token = typeof message.token === "string" ? message.token : "";
    this.clientWs = ws;
    const clientId = this.state.id.toString();

    if (requested) {
      // Custom subdomains require authentication
      if (!token) {
        ws.send(JSON.stringify({
          type: "register_error",
          error: "Authentication required for custom subdomains. Run 'wormhole login' first.",
        }));
        return;
      }

      const auth = await this.validateToken(token);
      if (!auth.valid) {
        ws.send(JSON.stringify({
          type: "register_error",
          error: "Invalid or expired token. Run 'wormhole login' to re-authenticate.",
        }));
        return;
      }

      // Validate format: 3-32 chars, alphanumeric + hyphens, no leading/trailing hyphens
      if (!/^[a-z0-9][a-z0-9-]{1,30}[a-z0-9]$/.test(requested) && !/^[a-z0-9]{3,32}$/.test(requested)) {
        ws.send(JSON.stringify({
          type: "register_error",
          error: "Invalid subdomain. Use 3-32 lowercase alphanumeric characters or hyphens.",
        }));
        return;
      }

      // Check reserved system subdomains
      const reserved = new Set(["www", "api", "relay", "admin", "mail", "app", "dashboard"]);
      if (reserved.has(requested)) {
        ws.send(JSON.stringify({
          type: "register_error",
          error: `Subdomain "${requested}" is reserved.`,
        }));
        return;
      }

      // Check if subdomain is reserved by another user
      const reservedRow = await this.env.DB.prepare(
        "SELECT user_id FROM reserved_subdomains WHERE subdomain = ?"
      ).bind(requested).first<{ user_id: string }>();
      if (reservedRow && reservedRow.user_id !== auth.githubId) {
        ws.send(JSON.stringify({
          type: "register_error",
          error: `Subdomain "${requested}" is reserved by another user.`,
        }));
        return;
      }

      // Check if subdomain is already in use by an active tunnel
      const active = await this.env.DB.prepare(
        "SELECT client_id FROM tunnels WHERE subdomain = ?"
      ).bind(requested).first();
      if (active) {
        ws.send(JSON.stringify({
          type: "register_error",
          error: `Subdomain "${requested}" is already in use.`,
        }));
        return;
      }

      this.subdomain = requested;
    } else {
      // Generate a random 6-char subdomain, retry on collision
      for (let i = 0; i < 5; i++) {
        const candidate = generateSubdomain();
        const existing = await this.env.DB.prepare(
          "SELECT 1 FROM tunnels WHERE subdomain = ?"
        ).bind(candidate).first();
        if (!existing) {
          this.subdomain = candidate;
          break;
        }
      }
      if (!this.subdomain) {
        this.subdomain = generateSubdomain() + generateSubdomain().slice(0, 2);
      }
    }

    await this.state.storage.put("subdomain", this.subdomain);
    await this.env.DB.prepare(
      "INSERT INTO tunnels (subdomain, client_id) VALUES (?, ?)"
    ).bind(this.subdomain, clientId).run();

    ws.send(JSON.stringify({
      type: "registered",
      subdomain: this.subdomain,
      url: `https://${this.subdomain}.wormhole.bar`,
    }));
  }

  private handleHttpResponse(message: HttpResponseMessage): void {
    const pending = this.pendingRequests.get(message.id);
    if (!pending) return;

    clearTimeout(pending.timeout);
    this.pendingRequests.delete(message.id);

    const bodyBytes = message.body
      ? Uint8Array.from(atob(message.body), (c) => c.charCodeAt(0))
      : null;

    const headers = new Headers(message.headers);
    pending.resolve(new Response(bodyBytes, {
      status: message.status,
      headers,
    }));
  }
}

function generateSubdomain(): string {
  const chars = "abcdefghijklmnopqrstuvwxyz0123456789";
  const bytes = new Uint8Array(6);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (b) => chars[b % chars.length]).join("");
}
