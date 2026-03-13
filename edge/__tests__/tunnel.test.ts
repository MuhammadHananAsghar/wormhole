import { describe, it, expect, beforeAll } from "vitest";
import { env } from "cloudflare:test";
import { Tunnel } from "../src/tunnel";

const DOMAIN = "wormhole.bar";

beforeAll(async () => {
  await env.DB.exec(
    "CREATE TABLE IF NOT EXISTS tunnels (subdomain TEXT PRIMARY KEY, client_id TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT (datetime('now')))"
  );
  await env.DB.exec(
    "CREATE TABLE IF NOT EXISTS users (github_id TEXT PRIMARY KEY, username TEXT NOT NULL, token TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT (datetime('now')))"
  );
  await env.DB.exec(
    "CREATE TABLE IF NOT EXISTS reserved_subdomains (subdomain TEXT PRIMARY KEY, user_id TEXT, reserved_at TEXT NOT NULL DEFAULT (datetime('now')))"
  );
});

describe("Tunnel Durable Object", () => {
  function getStub() {
    const id = env.TUNNEL.newUniqueId();
    return env.TUNNEL.get(id) as DurableObjectStub<Tunnel>;
  }

  describe("proxy requests without connected client", () => {
    it("returns 502 when no client is connected", async () => {
      const stub = getStub();
      const response = await stub.fetch(`https://test.${DOMAIN}/hello`);

      expect(response.status).toBe(502);
      const body = await response.json<{ error: string }>();
      expect(body.error).toContain("not connected");
    });
  });

  describe("tunnel registration", () => {
    it("returns 426 if register request is not a WebSocket upgrade", async () => {
      const stub = getStub();
      const response = await stub.fetch(`https://${DOMAIN}/_wormhole/register`);

      expect(response.status).toBe(426);
      const body = await response.json<{ error: string }>();
      expect(body.error).toContain("WebSocket");
    });

    it("accepts WebSocket upgrade and returns 101", async () => {
      const stub = getStub();
      const response = await stub.fetch(`https://${DOMAIN}/_wormhole/register`, {
        headers: { Upgrade: "websocket" },
      });

      expect(response.status).toBe(101);
      expect(response.webSocket).toBeDefined();
      response.webSocket!.accept();
      response.webSocket!.close();
    });

    it("sends registered message with subdomain after register message", async () => {
      const stub = getStub();
      const response = await stub.fetch(`https://${DOMAIN}/_wormhole/register`, {
        headers: { Upgrade: "websocket" },
      });

      const ws = response.webSocket!;
      ws.accept();

      const messagePromise = new Promise<string>((resolve) => {
        ws.addEventListener("message", (event) => {
          resolve(event.data as string);
        });
      });

      ws.send(JSON.stringify({ type: "register", protocol: "http" }));

      const msg = await messagePromise;
      const parsed = JSON.parse(msg);

      expect(parsed.type).toBe("registered");
      expect(parsed.subdomain).toMatch(/^[a-z0-9]{6}$/);
      expect(parsed.url).toBe(`https://${parsed.subdomain}.${DOMAIN}`);

      // Verify subdomain was persisted to D1
      const row = await env.DB.prepare(
        "SELECT * FROM tunnels WHERE subdomain = ?"
      ).bind(parsed.subdomain).first();
      expect(row).not.toBeNull();

      ws.close();
    });

    it("rejects custom subdomain without auth token", async () => {
      const stub = getStub();
      const response = await stub.fetch(`https://${DOMAIN}/_wormhole/register`, {
        headers: { Upgrade: "websocket" },
      });

      const ws = response.webSocket!;
      ws.accept();

      const messagePromise = new Promise<string>((resolve) => {
        ws.addEventListener("message", (event) => {
          resolve(event.data as string);
        });
      });

      ws.send(JSON.stringify({ type: "register", protocol: "http", subdomain: "myapp" }));

      const msg = await messagePromise;
      const parsed = JSON.parse(msg);

      expect(parsed.type).toBe("register_error");
      expect(parsed.error).toContain("Authentication required");

      ws.close();
    });

    it("rejects custom subdomain with invalid token", async () => {
      const stub = getStub();
      const response = await stub.fetch(`https://${DOMAIN}/_wormhole/register`, {
        headers: { Upgrade: "websocket" },
      });

      const ws = response.webSocket!;
      ws.accept();

      const messagePromise = new Promise<string>((resolve) => {
        ws.addEventListener("message", (event) => {
          resolve(event.data as string);
        });
      });

      ws.send(JSON.stringify({ type: "register", protocol: "http", subdomain: "myapp", token: "invalid-token" }));

      const msg = await messagePromise;
      const parsed = JSON.parse(msg);

      expect(parsed.type).toBe("register_error");
      expect(parsed.error).toContain("Invalid or expired token");

      ws.close();
    });

    it("accepts custom subdomain with valid token", async () => {
      // Insert a test user into D1
      const testToken = "test-token-12345";
      await env.DB.prepare(
        "INSERT OR REPLACE INTO users (github_id, username, token) VALUES (?, ?, ?)"
      ).bind("12345", "testuser", testToken).run();

      const stub = getStub();
      const response = await stub.fetch(`https://${DOMAIN}/_wormhole/register`, {
        headers: { Upgrade: "websocket" },
      });

      const ws = response.webSocket!;
      ws.accept();

      const messagePromise = new Promise<string>((resolve) => {
        ws.addEventListener("message", (event) => {
          resolve(event.data as string);
        });
      });

      ws.send(JSON.stringify({ type: "register", protocol: "http", subdomain: "myapp", token: testToken }));

      const msg = await messagePromise;
      const parsed = JSON.parse(msg);

      expect(parsed.type).toBe("registered");
      expect(parsed.subdomain).toBe("myapp");
      expect(parsed.url).toBe(`https://myapp.${DOMAIN}`);

      ws.close();
    });

    it("reclaims custom subdomain when prior tunnel is stale", async () => {
      const testToken = "test-token-999";
      await env.DB.prepare(
        "INSERT OR REPLACE INTO users (github_id, username, token) VALUES (?, ?, ?)"
      ).bind("999", "staleuser", testToken).run();
      await env.DB.prepare(
        "INSERT OR REPLACE INTO reserved_subdomains (subdomain, user_id) VALUES (?, ?)"
      ).bind("myapp", "999").run();

      const staleId = env.TUNNEL.newUniqueId();
      await env.DB.prepare(
        "INSERT OR REPLACE INTO tunnels (subdomain, client_id) VALUES (?, ?)"
      ).bind("myapp", staleId.toString()).run();

      const stub = getStub();
      const response = await stub.fetch(`https://${DOMAIN}/_wormhole/register`, {
        headers: { Upgrade: "websocket" },
      });

      const ws = response.webSocket!;
      ws.accept();

      const messagePromise = new Promise<string>((resolve) => {
        ws.addEventListener("message", (event) => {
          resolve(event.data as string);
        });
      });

      ws.send(JSON.stringify({ type: "register", protocol: "http", subdomain: "myapp", token: testToken }));

      const msg = await messagePromise;
      const parsed = JSON.parse(msg);

      expect(parsed.type).toBe("registered");
      expect(parsed.subdomain).toBe("myapp");

      const row = await env.DB.prepare(
        "SELECT client_id FROM tunnels WHERE subdomain = ?"
      ).bind("myapp").first<{ client_id: string }>();
      expect(row).not.toBeNull();
      expect(row!.client_id).not.toBe(staleId.toString());

      ws.close();
    });

    it("rejects reserved system subdomains", async () => {
      const testToken = "test-token-12345";
      await env.DB.prepare(
        "INSERT OR REPLACE INTO users (github_id, username, token) VALUES (?, ?, ?)"
      ).bind("12345", "testuser", testToken).run();

      const stub = getStub();
      const response = await stub.fetch(`https://${DOMAIN}/_wormhole/register`, {
        headers: { Upgrade: "websocket" },
      });

      const ws = response.webSocket!;
      ws.accept();

      const messagePromise = new Promise<string>((resolve) => {
        ws.addEventListener("message", (event) => {
          resolve(event.data as string);
        });
      });

      ws.send(JSON.stringify({ type: "register", protocol: "http", subdomain: "admin", token: testToken }));

      const msg = await messagePromise;
      const parsed = JSON.parse(msg);

      expect(parsed.type).toBe("register_error");
      expect(parsed.error).toContain("reserved");

      ws.close();
    });

    it("rejects invalid subdomain format", async () => {
      const testToken = "test-token-12345";
      await env.DB.prepare(
        "INSERT OR REPLACE INTO users (github_id, username, token) VALUES (?, ?, ?)"
      ).bind("12345", "testuser", testToken).run();

      const stub = getStub();
      const response = await stub.fetch(`https://${DOMAIN}/_wormhole/register`, {
        headers: { Upgrade: "websocket" },
      });

      const ws = response.webSocket!;
      ws.accept();

      const messagePromise = new Promise<string>((resolve) => {
        ws.addEventListener("message", (event) => {
          resolve(event.data as string);
        });
      });

      ws.send(JSON.stringify({ type: "register", protocol: "http", subdomain: "ab", token: testToken }));

      const msg = await messagePromise;
      const parsed = JSON.parse(msg);

      expect(parsed.type).toBe("register_error");
      expect(parsed.error).toContain("Invalid subdomain");

      ws.close();
    });
  });

  describe("HTTP proxying", () => {
    it("proxies request to connected client and returns response", async () => {
      const stub = getStub();

      // Connect a "client" via WebSocket
      const regResponse = await stub.fetch(`https://${DOMAIN}/_wormhole/register`, {
        headers: { Upgrade: "websocket" },
      });
      const ws = regResponse.webSocket!;
      ws.accept();

      // Wait for registration
      const regPromise = new Promise<void>((resolve) => {
        ws.addEventListener("message", () => resolve(), { once: true });
      });
      ws.send(JSON.stringify({ type: "register", protocol: "http" }));
      await regPromise;

      // Simulate client responding to proxied HTTP requests
      ws.addEventListener("message", (event) => {
        const req = JSON.parse(event.data as string);
        if (req.type === "http_request") {
          const responseBody = btoa(JSON.stringify({ hello: "world" }));
          ws.send(JSON.stringify({
            type: "http_response",
            id: req.id,
            status: 200,
            headers: { "Content-Type": "application/json" },
            body: responseBody,
          }));
        }
      });

      // Send an HTTP request to the tunnel
      const proxyResponse = await stub.fetch(`https://test.${DOMAIN}/api/hello`, {
        method: "GET",
        headers: { Host: `test.${DOMAIN}` },
      });

      expect(proxyResponse.status).toBe(200);
      const body = await proxyResponse.json<{ hello: string }>();
      expect(body.hello).toBe("world");

      ws.close();
    });

    it("injects x-forwarded headers in proxied requests", async () => {
      const stub = getStub();

      const regResponse = await stub.fetch(`https://${DOMAIN}/_wormhole/register`, {
        headers: { Upgrade: "websocket" },
      });
      const ws = regResponse.webSocket!;
      ws.accept();

      const regPromise = new Promise<void>((resolve) => {
        ws.addEventListener("message", () => resolve(), { once: true });
      });
      ws.send(JSON.stringify({ type: "register", protocol: "http" }));
      await regPromise;

      // Capture the proxied request headers
      const headersPromise = new Promise<Record<string, unknown>>((resolve) => {
        ws.addEventListener("message", (event) => {
          const req = JSON.parse(event.data as string);
          if (req.type === "http_request") {
            resolve(req.headers);
            ws.send(JSON.stringify({
              type: "http_response",
              id: req.id,
              status: 200,
              headers: {},
              body: btoa("ok"),
            }));
          }
        });
      });

      await stub.fetch(`https://test.${DOMAIN}/api/test`, {
        method: "GET",
        headers: {
          Host: `test.${DOMAIN}`,
          "cf-connecting-ip": "1.2.3.4",
        },
      });

      const headers = await headersPromise;
      expect(headers["x-forwarded-proto"]).toBe("https");
      expect(headers["x-forwarded-host"]).toBe(`test.${DOMAIN}`);
      expect(headers["x-forwarded-for"]).toBe("1.2.3.4");

      ws.close();
    });
  });

  describe("visitor WebSocket passthrough", () => {
    it("returns 502 for WebSocket upgrade without connected client", async () => {
      const stub = getStub();
      const response = await stub.fetch(`https://test.${DOMAIN}/ws`, {
        headers: { Upgrade: "websocket" },
      });

      expect(response.status).toBe(502);
    });
  });
});
