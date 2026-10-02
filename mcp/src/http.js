// Streamable HTTP, stateless: every POST gets a fresh server and transport,
// so any instance can answer any request (Cloud Functions scale out and back
// to zero). A browser opening the URL is sent to the instructions page.

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

export function mcpHandler({ store, baseUrl, fetchImpl, limiter = rateLimiter() }) {
  return async (req, res) => {
    res.setHeader("Access-Control-Allow-Origin", "*");
    res.setHeader("Access-Control-Allow-Headers", "content-type, mcp-session-id, mcp-protocol-version, authorization, last-event-id");
    res.setHeader("Access-Control-Expose-Headers", "mcp-session-id, mcp-protocol-version");
    res.setHeader("Access-Control-Allow-Methods", "GET, POST, DELETE, OPTIONS");
    if (req.method === "OPTIONS") return res.status(204).end();
    if (req.method === "GET" && !String(req.headers.accept || "").includes("text/event-stream")) {
      return res.redirect(302, `${baseUrl}/connect.html`);
    }
    if (req.method !== "POST") {
      return res.status(405).set("Allow", "POST").json({ jsonrpc: "2.0", error: { code: -32000, message: "Method not allowed: this server is stateless, POST only." }, id: null });
    }
    const ip = String(req.headers["x-forwarded-for"] || req.ip || "").split(",")[0].trim();
    const server = createServer({
      store, baseUrl, fetchImpl,
      limit: (kind) => (kind === "create_presentation" || kind === "update_presentation" ? limiter(ip) : null),
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
  };
}
