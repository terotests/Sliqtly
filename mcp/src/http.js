// Streamable HTTP, stateless: every POST gets a fresh server and transport,
// so any instance can answer any request (Cloud Functions scale out and back
// to zero). A browser opening the URL is sent to the instructions page.
// Sign-in is optional: see oauth.js.

import express from "express";
import { StreamableHTTPServerTransport } from "@modelcontextprotocol/sdk/server/streamableHttp.js";
import { createServer } from "./server.js";

// Writes per caller: a sliding window, per instance. Enough to stop a loop,
// not a quota system.
export function rateLimiter({ max = 60, windowMs = 10 * 60 * 1000 } = {}) {
  const hits = new Map();
  return (who) => {
    const now = Date.now();
    const list = (hits.get(who) || []).filter((t) => now - t < windowMs);
    if (list.length >= max) return `Too many presentations from here in a short time; try again in a few minutes.`;
    list.push(now);
    hits.set(who, list);
    if (hits.size > 5000) for (const [k, v] of hits) if (!v.some((t) => now - t < windowMs)) hits.delete(k);
    return null;
  };
}

// the tools that store something, counted by the limiter
const WRITES = new Set(["create_presentation", "update_presentation", "bind_chart_data"]);

// The site's own addresses: the OAuth issuer and the resource follow the
// one the client used, so both domains work.
const SITES = ["https://sliqtly.com", "https://sliqtly.web.app"];

function originOf(req, baseUrl, trustHost) {
  const host = String(req.headers["x-forwarded-host"] || req.headers.host || "").split(",")[0].trim();
  const https = `https://${host}`;
  if (SITES.includes(https) || https === baseUrl) return https;
  if (trustHost && host) return `${req.protocol}://${host}`;
  return baseUrl;
}

function cors(res) {
  res.setHeader("Access-Control-Allow-Origin", "*");
  res.setHeader("Access-Control-Allow-Headers", "content-type, mcp-session-id, mcp-protocol-version, authorization, last-event-id");
  res.setHeader("Access-Control-Expose-Headers", "mcp-session-id, mcp-protocol-version, www-authenticate");
  res.setHeader("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS");
}

// The whole server as an Express app: /mcp, and with `oauth` the sign-in
// endpoints. Cloud Functions hands it the request with its path as Hosting
// received it.
export function createApp({ store, baseUrl, fetchImpl, oauth = null, limiter = rateLimiter(), trustHost = false }) {
  const app = express();
  app.disable("x-powered-by");
  app.use(express.json({ limit: "40mb" }));
  app.use(express.urlencoded({ extended: false, limit: "100kb" }));
  app.use((req, res, next) => { cors(res); if (req.method === "OPTIONS") return res.status(204).end(); next(); });

  if (oauth) {
    const prm = (req, res) => res.json(oauth.resourceMetadata(originOf(req, baseUrl, trustHost)));
    app.get("/.well-known/oauth-protected-resource", prm);
    app.get("/.well-known/oauth-protected-resource/mcp", prm);
    app.get(["/.well-known/oauth-authorization-server", "/.well-known/openid-configuration"], (req, res) => res.json(oauth.metadata(originOf(req, baseUrl, trustHost))));
    const send = (res, r) => (r.redirect ? res.redirect(302, r.redirect) : r.text ? res.status(r.status).type("text/plain").send(r.text) : res.status(r.status).set("Cache-Control", "no-store").json(r.json));
    const wrap = (fn) => async (req, res) => {
      try { send(res, await fn(req)); } catch (e) {
        console.error("oauth failed", e);
        res.status(500).json({ error: "server_error" });
      }
    };
    app.post("/oauth/register", wrap((req) => oauth.register(req.body)));
    app.get("/oauth/authorize", wrap((req) => oauth.authorize(req.query, originOf(req, baseUrl, trustHost))));
    app.post("/oauth/approve", wrap((req) => oauth.approve(req.body)));
    app.post("/oauth/token", wrap((req) => oauth.token(req.body)));
  }

  app.all("/mcp", async (req, res) => {
    if (req.method === "GET" && !String(req.headers.accept || "").includes("text/event-stream")) {
      return res.redirect(302, `${baseUrl}/connect.html`);
    }
    if (req.method !== "POST") {
      return res.status(405).set("Allow", "POST").json({ jsonrpc: "2.0", error: { code: -32000, message: "Method not allowed: this server is stateless, POST only." }, id: null });
    }
    // no token: anonymous, as before; a token that does not hold: 401, so
    // the client refreshes it or signs in again
    let user = null;
    if (oauth) {
      user = await oauth.who(req.headers.authorization);
      if (user === false) {
        const origin = originOf(req, baseUrl, trustHost);
        res.set("WWW-Authenticate", `Bearer error="invalid_token", resource_metadata="${origin}/.well-known/oauth-protected-resource/mcp"`);
        return res.status(401).json({ jsonrpc: "2.0", error: { code: -32001, message: "The sign-in has expired." }, id: null });
      }
    }
    const ip = String(req.headers["x-forwarded-for"] || req.ip || "").split(",")[0].trim();
    const server = createServer({
      store, baseUrl, fetchImpl, user,
      signIn: oauth ? `${originOf(req, baseUrl, trustHost)}/.well-known/oauth-protected-resource/mcp` : null,
      limit: (kind) => (WRITES.has(kind) ? limiter(user ? `uid:${user.uid}` : ip) : null),
    });
    const transport = new StreamableHTTPServerTransport({ sessionIdGenerator: undefined, enableJsonResponse: true });
    res.on("close", () => { transport.close(); server.close(); });
    try {
      await server.connect(transport);
      await transport.handleRequest(req, res, req.body);
    } catch (e) {
      console.error("mcp request failed", e);
      if (!res.headersSent) res.status(500).json({ jsonrpc: "2.0", error: { code: -32603, message: "Internal error" }, id: null });
    }
  });
  return app;
}
