"use strict";

const http = require("node:http");
const https = require("node:https");

const DEFAULT_TIMEOUT_MS = 15000;
const SAFE_ASSET_ID_RE = /^[A-Za-z0-9._-]+$/;
const PASS_THROUGH_HEADERS = [
	"content-type",
	"content-length",
	"etag",
	"cache-control",
	"last-modified"
];

function pipeUpstreamAsset(req, res, source, upstreamSubpath, logger) {
	const assetId = req.params && req.params.id ? String(req.params.id) : "";
	if (!assetId || !SAFE_ASSET_ID_RE.test(assetId)) {
		res.statusCode = 400;
		res.end("Invalid asset id");
		return;
	}
	if (!source || !source.url) {
		res.statusCode = 503;
		res.end("Media source not configured");
		return;
	}

	let target;
	try {
		const base = source.url.endsWith("/") ? source.url : `${source.url}/`;
		target = new URL(`api/assets/${encodeURIComponent(assetId)}/${upstreamSubpath}`, base);
	} catch (_err) {
		res.statusCode = 500;
		res.end("Invalid upstream URL");
		return;
	}

	const transport = target.protocol === "https:" ? https : http;
	const headers = {};
	if (source.apiKey) {
		headers["x-api-key"] = source.apiKey;
	}
	if (req.headers && req.headers["if-none-match"]) {
		headers["if-none-match"] = req.headers["if-none-match"];
	}

	let upstreamResRef = null;
	let finished = false;

	const upstreamReq = transport.request(
		target,
		{
			method: "GET",
			headers,
			timeout: source.timeout || DEFAULT_TIMEOUT_MS
		},
		(upstreamRes) => {
			upstreamResRef = upstreamRes;
			res.statusCode = upstreamRes.statusCode || 502;
			for (const headerName of PASS_THROUGH_HEADERS) {
				const val = upstreamRes.headers[headerName];
				if (val !== undefined) {
					res.setHeader(headerName, val);
				}
			}
			if (typeof res.flushHeaders === "function") {
				res.flushHeaders();
			}
			upstreamRes.on("end", () => {
				finished = true;
			});
			upstreamRes.on("error", () => {
				if (!res.destroyed) {
					res.destroy();
				}
			});
			upstreamRes.pipe(res);
		}
	);

	const abortUpstream = () => {
		if (finished || res.writableEnded) {
			return;
		}
		finished = true;
		if (upstreamResRef && !upstreamResRef.destroyed) {
			upstreamResRef.destroy();
		}
		if (!upstreamReq.destroyed) {
			upstreamReq.destroy();
		}
	};

	req.on("aborted", abortUpstream);
	req.on("close", () => {
		if (!res.writableEnded) {
			abortUpstream();
		}
	});
	res.on("close", () => {
		if (!res.writableEnded) {
			abortUpstream();
		}
	});

	upstreamReq.on("timeout", () => {
		upstreamReq.destroy(new Error("Upstream photo-api request timed out"));
	});

	upstreamReq.on("error", (err) => {
		if (finished) {
			return;
		}
		finished = true;
		if (logger && typeof logger.warn === "function") {
			logger.warn(`[MMM-MediaBackground] Upstream pipe error for ${assetId}/${upstreamSubpath}: ${err.message}`);
		}
		if (!res.headersSent) {
			res.statusCode = 502;
			res.end("Bad gateway");
		} else if (!res.destroyed) {
			res.destroy();
		}
	});

	upstreamReq.end();
}

function registerMediaRoutes(expressApp, resolveSource, logger = null) {
	if (!expressApp || typeof expressApp.get !== "function") {
		return;
	}
	expressApp.get("/mediabackground/preview/:id", (req, res) => {
		pipeUpstreamAsset(req, res, resolveSource(req), "thumbnail", logger);
	});
	expressApp.get("/mediabackground/backdrop/:id", (req, res) => {
		pipeUpstreamAsset(req, res, resolveSource(req), "backdrop", logger);
	});
}

module.exports = { registerMediaRoutes };
