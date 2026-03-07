import { describe, it, expect, beforeAll } from "vitest";
import {
  env,
  createExecutionContext,
  waitOnExecutionContext,
} from "cloudflare:test";
import worker, { extractSubdomain, isRegisterRequest, isAuthRequest } from "../src/index";

const DOMAIN = "wormhole.bar";

beforeAll(async () => {
  await env.DB.exec(
    "CREATE TABLE IF NOT EXISTS tunnels (subdomain TEXT PRIMARY KEY, client_id TEXT NOT NULL, created_at TEXT NOT NULL DEFAULT (datetime('now')))"
  );
});

describe("extractSubdomain", () => {
  it("extracts subdomain from valid host", () => {
    expect(extractSubdomain(`dummy.${DOMAIN}`)).toBe("dummy");
    expect(extractSubdomain(`k7x9m2.${DOMAIN}`)).toBe("k7x9m2");
    expect(extractSubdomain(`myapp.${DOMAIN}`)).toBe("myapp");
  });

  it("returns null for bare domain", () => {
    expect(extractSubdomain(DOMAIN)).toBeNull();
  });

  it("returns null for unrelated domain", () => {
    expect(extractSubdomain("example.com")).toBeNull();
    expect(extractSubdomain("foo.example.com")).toBeNull();
  });

  it("returns null for nested subdomains", () => {
    expect(extractSubdomain(`a.b.${DOMAIN}`)).toBeNull();
  });

  it("strips port before extracting", () => {
    expect(extractSubdomain(`test.${DOMAIN}:8080`)).toBe("test");
  });

  it("returns null for empty subdomain", () => {
    expect(extractSubdomain(`.${DOMAIN}`)).toBeNull();
  });
});

describe("isRegisterRequest", () => {
  it("matches register path on bare domain", () => {
    const req = new Request(`https://${DOMAIN}/_wormhole/register`);
    expect(isRegisterRequest(req, DOMAIN)).toBe(true);
  });

  it("matches register path on relay subdomain", () => {
    const req = new Request(`https://relay.${DOMAIN}/_wormhole/register`);
    expect(isRegisterRequest(req, `relay.${DOMAIN}`)).toBe(true);
  });

  it("rejects non-register paths", () => {
    const req = new Request(`https://${DOMAIN}/other`);
    expect(isRegisterRequest(req, DOMAIN)).toBe(false);
  });

  it("rejects register path on tunnel subdomain", () => {
    const req = new Request(`https://abc123.${DOMAIN}/_wormhole/register`);
    expect(isRegisterRequest(req, `abc123.${DOMAIN}`)).toBe(false);
  });
});

describe("isAuthRequest", () => {
  it("matches auth/github on bare domain", () => {
    const req = new Request(`https://${DOMAIN}/_wormhole/auth/github`);
    expect(isAuthRequest(req, DOMAIN)).toBe(true);
  });

  it("matches auth/callback on bare domain", () => {
    const req = new Request(`https://${DOMAIN}/_wormhole/auth/callback`);
    expect(isAuthRequest(req, DOMAIN)).toBe(true);
  });

  it("matches auth paths on relay subdomain", () => {
    const req = new Request(`https://relay.${DOMAIN}/_wormhole/auth/github`);
    expect(isAuthRequest(req, `relay.${DOMAIN}`)).toBe(true);
  });

  it("rejects auth paths on tunnel subdomain", () => {
    const req = new Request(`https://abc123.${DOMAIN}/_wormhole/auth/github`);
    expect(isAuthRequest(req, `abc123.${DOMAIN}`)).toBe(false);
  });

  it("rejects non-auth paths on bare domain", () => {
    const req = new Request(`https://${DOMAIN}/other`);
    expect(isAuthRequest(req, DOMAIN)).toBe(false);
  });
});

describe("Worker fetch handler", () => {
  it("returns 400 for bare domain request", async () => {
    const request = new Request(`https://${DOMAIN}/`);
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(400);
    const body = await response.json<{ error: string }>();
    expect(body.error).toBeDefined();
  });

  it("returns 400 for reserved subdomain (www)", async () => {
    const request = new Request(`https://www.${DOMAIN}/`, {
      headers: { Host: `www.${DOMAIN}` },
    });
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(400);
  });

  it("returns 404 for unknown subdomain (not in D1)", async () => {
    const request = new Request(`https://k7x9m2.${DOMAIN}/some/path`, {
      headers: { Host: `k7x9m2.${DOMAIN}` },
    });
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(404);
    const body = await response.json<{ error: string }>();
    expect(body.error).toContain("not found");
  });

  it("redirects auth/github to GitHub OAuth", async () => {
    const request = new Request(`https://${DOMAIN}/_wormhole/auth/github?port=12345`);
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(302);
    const location = response.headers.get("Location");
    expect(location).toContain("github.com/login/oauth/authorize");
    expect(location).toContain("redirect_uri");
    expect(location).toContain("state=");
  });

  it("returns 400 for auth/callback without code", async () => {
    const request = new Request(`https://${DOMAIN}/_wormhole/auth/callback`);
    const ctx = createExecutionContext();
    const response = await worker.fetch(request, env, ctx);
    await waitOnExecutionContext(ctx);

    expect(response.status).toBe(400);
    const body = await response.json<{ error: string }>();
    expect(body.error).toContain("Missing code or state");
  });
});
