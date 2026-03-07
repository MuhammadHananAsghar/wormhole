import { Tunnel } from "./tunnel";

export { Tunnel };

export interface Env {
  TUNNEL: DurableObjectNamespace;
  DB: D1Database;
  GITHUB_CLIENT_ID: string;
  GITHUB_CLIENT_SECRET: string;
  AUTH_SECRET: string; // HMAC key for signing tokens
}

const RESERVED_SUBDOMAINS = new Set(["www", "api", "relay", "admin", "mail"]);
const BASE_DOMAIN = "wormhole.bar";
const REGISTER_PATH = "/_wormhole/register";
const AUTH_GITHUB_PATH = "/_wormhole/auth/github";
const AUTH_CALLBACK_PATH = "/_wormhole/auth/callback";

export function extractSubdomain(host: string): string | null {
  // Remove port if present
  const hostname = host.split(":")[0];

  // Extract subdomain: "dummy.wormhole.bar" -> "dummy"
  if (!hostname.endsWith(`.${BASE_DOMAIN}`)) {
    if (hostname === BASE_DOMAIN) return null;
    return null;
  }

  const subdomain = hostname.slice(0, -(BASE_DOMAIN.length + 1));
  if (!subdomain || subdomain.includes(".")) return null;

  return subdomain;
}

export function isRegisterRequest(request: Request, host: string): boolean {
  const url = new URL(request.url);
  const hostname = host.split(":")[0];
  return (
    url.pathname === REGISTER_PATH &&
    (hostname === BASE_DOMAIN || hostname === `relay.${BASE_DOMAIN}`)
  );
}

function jsonResponse(body: object, status: number): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

export function isAuthRequest(request: Request, host: string): boolean {
  const url = new URL(request.url);
  const hostname = host.split(":")[0];
  const isMainDomain = hostname === BASE_DOMAIN || hostname === `relay.${BASE_DOMAIN}`;
  return isMainDomain && (url.pathname === AUTH_GITHUB_PATH || url.pathname === AUTH_CALLBACK_PATH);
}

async function handleAuth(request: Request, env: Env): Promise<Response> {
  const url = new URL(request.url);

  if (url.pathname === AUTH_GITHUB_PATH) {
    // Store the CLI callback port in state param
    const port = url.searchParams.get("port") || "";
    const state = btoa(JSON.stringify({ port }));
    const githubUrl = new URL("https://github.com/login/oauth/authorize");
    githubUrl.searchParams.set("client_id", env.GITHUB_CLIENT_ID);
    githubUrl.searchParams.set("redirect_uri", `https://${BASE_DOMAIN}${AUTH_CALLBACK_PATH}`);
    githubUrl.searchParams.set("scope", "read:user");
    githubUrl.searchParams.set("state", state);
    return Response.redirect(githubUrl.toString(), 302);
  }

  if (url.pathname === AUTH_CALLBACK_PATH) {
    const code = url.searchParams.get("code");
    const port = url.searchParams.get("state") || ""; // state = CLI local port
    if (!code) {
      return jsonResponse({ error: "Missing code parameter" }, 400);
    }

    // Exchange code for GitHub access token
    const tokenResp = await fetch("https://github.com/login/oauth/access_token", {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        "Accept": "application/json",
      },
      body: JSON.stringify({
        client_id: env.GITHUB_CLIENT_ID,
        client_secret: env.GITHUB_CLIENT_SECRET,
        code,
      }),
    });

    const tokenData = await tokenResp.json<{ access_token?: string; error?: string }>();
    if (!tokenData.access_token) {
      return jsonResponse({ error: tokenData.error || "Failed to get access token" }, 400);
    }

    // Get GitHub user info
    const userResp = await fetch("https://api.github.com/user", {
      headers: {
        "Authorization": `Bearer ${tokenData.access_token}`,
        "User-Agent": "wormhole-relay",
        "Accept": "application/json",
      },
    });

    const userData = await userResp.json<{ login?: string; id?: number }>();
    if (!userData.login || !userData.id) {
      return jsonResponse({ error: "Failed to get user info" }, 400);
    }

    // Generate a wormhole auth token (HMAC-signed)
    const payload = JSON.stringify({
      sub: String(userData.id),
      username: userData.login,
      iat: Math.floor(Date.now() / 1000),
    });
    const encoder = new TextEncoder();
    const key = await crypto.subtle.importKey(
      "raw",
      encoder.encode(env.AUTH_SECRET),
      { name: "HMAC", hash: "SHA-256" },
      false,
      ["sign"],
    );
    const signature = await crypto.subtle.sign("HMAC", key, encoder.encode(payload));
    const token = btoa(payload) + "." + btoa(String.fromCharCode(...new Uint8Array(signature)));

    // Upsert user in D1
    await env.DB.prepare(
      "INSERT INTO users (github_id, username, token) VALUES (?, ?, ?) ON CONFLICT(github_id) DO UPDATE SET username = ?, token = ?"
    ).bind(String(userData.id), userData.login, token, userData.login, token).run();

    // Redirect back to CLI local server
    if (port) {
      const callbackUrl = `http://localhost:${port}/callback?token=${encodeURIComponent(token)}&username=${encodeURIComponent(userData.login)}`;
      return Response.redirect(callbackUrl, 302);
    }

    // Fallback: show token in page (for manual copy)
    return new Response(
      `<!DOCTYPE html><html><body style="font-family:monospace;padding:2rem">
       <h2>Logged in as ${userData.login}</h2>
       <p>Copy this token to your terminal:</p>
       <pre style="background:#f0f0f0;padding:1rem;border-radius:4px">${token}</pre>
       <p>You can close this window.</p></body></html>`,
      { status: 200, headers: { "Content-Type": "text/html" } },
    );
  }

  return jsonResponse({ error: "Not found" }, 404);
}

export default {
  async fetch(request: Request, env: Env, ctx: ExecutionContext): Promise<Response> {
    const host = request.headers.get("Host") || new URL(request.url).host;

    // Handle auth requests
    if (isAuthRequest(request, host)) {
      return handleAuth(request, env);
    }

    // Handle tunnel registration (WebSocket upgrade to create new tunnel)
    if (isRegisterRequest(request, host)) {
      // Create a new DO instance for this tunnel
      const id = env.TUNNEL.newUniqueId();
      const stub = env.TUNNEL.get(id);
      return stub.fetch(request);
    }

    // Extract subdomain for proxied requests
    const subdomain = extractSubdomain(host);

    if (!subdomain) {
      return jsonResponse(
        { error: "Missing or invalid subdomain. Use <subdomain>.wormhole.bar" },
        400
      );
    }

    if (RESERVED_SUBDOMAINS.has(subdomain)) {
      return jsonResponse(
        { error: "Missing or invalid subdomain. Use <subdomain>.wormhole.bar" },
        400
      );
    }

    // Look up subdomain in D1 to find the DO that owns this tunnel
    const row = await env.DB.prepare(
      "SELECT client_id FROM tunnels WHERE subdomain = ?"
    ).bind(subdomain).first<{ client_id: string }>();

    if (!row) {
      return jsonResponse({ error: "Tunnel not found" }, 404);
    }

    // Route to the Durable Object that holds this tunnel's WebSocket
    const id = env.TUNNEL.idFromString(row.client_id);
    const stub = env.TUNNEL.get(id);
    return stub.fetch(request);
  },
};
