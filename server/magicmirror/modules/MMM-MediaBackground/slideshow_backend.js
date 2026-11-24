"use strict";

const http = require("node:http");
const https = require("node:https");

const DEFAULT_SLIDESHOW_SPEED_MS = 12000;
const DEFAULT_TRANSITION_SPEED = "1.2s";
const DEFAULT_DATE_FORMAT = "DD-MM-YYYY dddd HH:mm";
const DEFAULT_INFO_FIELDS = ["date", "geo", "count", "album"];
const DEFAULT_TIMEOUT_MS = 15000;
const POOL_MAX_AGE_MS = 24 * 60 * 60 * 1000;
const EMPTY_RETRY_MS = 60 * 1000;

const VALID_MODES = new Set(["anniversary", "random", "metadata", "search", "memory"]);
const VALID_INFO_FIELDS = new Set(["date", "geo", "count", "album", "since", "name"]);
const VALID_POSITIONS = new Set(["bottomRight", "bottomLeft", "topRight", "topLeft"]);
const SAFE_ASSET_ID_RE = /^[A-Za-z0-9._-]+$/;

function normalizeInfoFields(rawFields, fallback = DEFAULT_INFO_FIELDS) {
	if (rawFields === undefined || rawFields === null) {
		return [...fallback];
	}
	let tokens = [];
	if (Array.isArray(rawFields)) {
		tokens = rawFields.map((item) => String(item ?? "").trim().toLowerCase());
	} else if (typeof rawFields === "string") {
		tokens = rawFields
			.toLowerCase()
			.split(/[\s,]+/)
			.map((s) => s.trim());
	}
	const filtered = tokens.filter((t) => VALID_INFO_FIELDS.has(t));
	return filtered.length > 0 ? filtered : [...fallback];
}

function clampInt(value, min, max, fallback) {
	const num = Number(value);
	if (!Number.isFinite(num)) {
		return fallback;
	}
	const intVal = Math.trunc(num);
	if (intVal < min || intVal > max) {
		return fallback;
	}
	return intVal;
}

function nonNegativeInt(value, fallback) {
	if (value === undefined || value === null || value === "") {
		return fallback;
	}
	const num = Number(value);
	if (!Number.isFinite(num) || num < 0) {
		return fallback;
	}
	return Math.trunc(num);
}

function normalizeMediaType(source) {
	const raw = source.mediaType ?? (source.query && source.query.type) ?? "ALL";
	const upper = String(raw).trim().toUpperCase();
	if (upper === "IMAGE" || upper === "VIDEO") {
		return upper;
	}
	return "ALL";
}

function normalizeSource(rawSource, baseSource, moduleDefaults, currentYear) {
	const merged = Object.assign({}, baseSource || {}, rawSource || {});
	const rawUrl = String(merged.url ?? merged.immichUrl ?? "").trim().replace(/\/+$/, "");
	const rawMode = String(merged.mode ?? "anniversary").trim().toLowerCase();
	const mode = VALID_MODES.has(rawMode) ? (rawMode === "search" ? "metadata" : rawMode) : "anniversary";

	const startYear = clampInt(merged.anniversaryStartYear, 1900, 9999, 2010);
	const defaultEndYear = Math.max(startYear, currentYear);
	const endYear = clampInt(merged.anniversaryEndYear, startYear, 9999, defaultEndYear);

	const rawQuery = merged.query && typeof merged.query === "object" && !Array.isArray(merged.query)
		? Object.assign({}, merged.query)
		: null;

	const fallbackQuerySize = rawQuery && Number.isFinite(Number(rawQuery.size))
		? clampInt(rawQuery.size, 1, 1000, 100)
		: 100;
	const querySize = clampInt(merged.querySize, 1, 1000, fallbackQuerySize);

	const rawSort = merged.sortBy ?? merged.sortImagesBy;
	const defaultSort = mode === "anniversary" ? "random" : "none";
	const sortBy = rawSort ? String(rawSort).trim().toLowerCase() : defaultSort;

	const rawFields = merged.infoFields ?? merged.imageInfo;

	return {
		url: rawUrl,
		apiKey: String(merged.apiKey ?? ""),
		mode,
		timeout: clampInt(merged.timeout ?? merged.immichTimeout, 1000, 120000, DEFAULT_TIMEOUT_MS),
		anniversaryDatesBack: nonNegativeInt(merged.anniversaryDatesBack, 45),
		anniversaryDatesForward: nonNegativeInt(merged.anniversaryDatesForward, 45),
		anniversaryStartYear: startYear,
		anniversaryEndYear: endYear,
		numDaysToInclude: clampInt(merged.numDaysToInclude, 1, 365, 7),
		querySize,
		query: rawQuery,
		mediaType: normalizeMediaType(merged),
		sortBy,
		sortDescending: Boolean(merged.sortDescending ?? merged.sortImagesDescending ?? false),
		slideshowSpeed: clampInt(merged.slideshowSpeed, 1000, 86400000, moduleDefaults.slideshowSpeed),
		dateFormat: String(merged.dateFormat || moduleDefaults.dateFormat),
		infoFields: rawFields !== undefined ? normalizeInfoFields(rawFields, moduleDefaults.infoFields) : [...moduleDefaults.infoFields]
	};
}

function normalizeConfig(rawConfig = {}, options = {}) {
	const now = options.now instanceof Date ? options.now : new Date();
	const currentYear = now.getUTCFullYear();

	const slideshowSpeed = clampInt(rawConfig.slideshowSpeed, 1000, 86400000, DEFAULT_SLIDESHOW_SPEED_MS);
	const transitionDisabled = rawConfig.transitionImages === false;
	const transitionSpeed = transitionDisabled
		? "0s"
		: String(rawConfig.transitionSpeed ?? DEFAULT_TRANSITION_SPEED).trim() || DEFAULT_TRANSITION_SPEED;

	const backgroundSize = String(rawConfig.backgroundSize ?? "contain").trim().toLowerCase() === "cover"
		? "cover"
		: "contain";
	const backgroundPosition = String(rawConfig.backgroundPosition ?? "center").trim() || "center";

	const showBlurredBackdrop = rawConfig.showBlurredBackdrop !== undefined
		? Boolean(rawConfig.showBlurredBackdrop)
		: (rawConfig.showBlurredImageForBlackBars !== undefined
			? Boolean(rawConfig.showBlurredImageForBlackBars)
			: true);

	const showMediaInfo = rawConfig.showMediaInfo !== undefined
		? Boolean(rawConfig.showMediaInfo)
		: (rawConfig.showImageInfo !== undefined ? Boolean(rawConfig.showImageInfo) : true);

	const infoFields = normalizeInfoFields(rawConfig.infoFields ?? rawConfig.imageInfo, DEFAULT_INFO_FIELDS);
	const dateFormat = String(rawConfig.dateFormat ?? DEFAULT_DATE_FORMAT).trim() || DEFAULT_DATE_FORMAT;

	const rawPos = String(rawConfig.infoPosition ?? rawConfig.imageInfoLocation ?? "bottomRight").trim();
	const infoPosition = VALID_POSITIONS.has(rawPos) ? rawPos : "bottomRight";

	const cyclicSources = Boolean(rawConfig.cyclicSources ?? rawConfig.cyclicConfigs ?? false);

	const moduleDefaults = {
		slideshowSpeed,
		dateFormat,
		infoFields
	};

	let rawSources = [];
	if (Array.isArray(rawConfig.sources) && rawConfig.sources.length > 0) {
		rawSources = rawConfig.sources;
	} else if (Array.isArray(rawConfig.immichConfigs) && rawConfig.immichConfigs.length > 0) {
		rawSources = rawConfig.immichConfigs;
	} else if (rawConfig.url || rawConfig.immichUrl || rawConfig.apiKey) {
		rawSources = [rawConfig];
	}

	const sources = [];
	let inheritedBase = null;
	for (let i = 0; i < rawSources.length; i++) {
		const entry = rawSources[i] || {};
		if (i === 0) {
			const firstNormalized = normalizeSource(entry, null, moduleDefaults, currentYear);
			sources.push(firstNormalized);
			inheritedBase = {
				url: entry.url ?? entry.immichUrl,
				apiKey: entry.apiKey,
				timeout: entry.timeout ?? entry.immichTimeout
			};
		} else {
			sources.push(normalizeSource(entry, inheritedBase, moduleDefaults, currentYear));
		}
	}

	const activeSourceIndex = clampInt(
		rawConfig.activeSourceIndex ?? rawConfig.activeImmichConfigIndex,
		0,
		Math.max(0, sources.length - 1),
		0
	);

	return {
		slideshowSpeed,
		transitionSpeed,
		backgroundSize,
		backgroundPosition,
		showBlurredBackdrop,
		showMediaInfo,
		infoFields,
		dateFormat,
		infoPosition,
		cyclicSources,
		activeSourceIndex,
		sources
	};
}

function mergeServerSecrets(clientConfig, serverConfig) {
	if (!serverConfig || typeof serverConfig !== "object") {
		return clientConfig || {};
	}
	const merged = Object.assign({}, serverConfig, clientConfig || {});
	const serverSources = Array.isArray(serverConfig.sources)
		? serverConfig.sources
		: (Array.isArray(serverConfig.immichConfigs) ? serverConfig.immichConfigs : []);
	const clientSourcesKey = Array.isArray(merged.sources) ? "sources" : (Array.isArray(merged.immichConfigs) ? "immichConfigs" : null);

	if (clientSourcesKey && serverSources.length > 0) {
		merged[clientSourcesKey] = merged[clientSourcesKey].map((src, idx) => {
			const srvSrc = serverSources[idx] || serverSources[0] || {};
			const copy = Object.assign({}, srvSrc, src);
			if (!copy.apiKey || String(copy.apiKey).includes("**SECRET_")) {
				copy.apiKey = srvSrc.apiKey ?? serverConfig.apiKey ?? "";
			}
			return copy;
		});
	}
	return merged;
}

function requestJson(baseUrl, pathAndQuery, { method = "GET", apiKey = "", body = null, timeout = DEFAULT_TIMEOUT_MS } = {}) {
	return new Promise((resolve, reject) => {
		let target;
		try {
			target = new URL(pathAndQuery, baseUrl.endsWith("/") ? baseUrl : `${baseUrl}/`);
		} catch (err) {
			reject(err);
			return;
		}

		const transport = target.protocol === "https:" ? https : http;
		const payloadBuf = body !== null ? Buffer.from(JSON.stringify(body), "utf8") : null;
		const headers = {
			Accept: "application/json"
		};
		if (apiKey) {
			headers["x-api-key"] = apiKey;
		}
		if (payloadBuf) {
			headers["Content-Type"] = "application/json";
			headers["Content-Length"] = String(payloadBuf.length);
		}

		const req = transport.request(
			target,
			{ method, headers, timeout },
			(res) => {
				const chunks = [];
				res.on("data", (chunk) => chunks.push(chunk));
				res.on("end", () => {
					const rawText = Buffer.concat(chunks).toString("utf8");
					if (res.statusCode < 200 || res.statusCode >= 300) {
						reject(new Error(`HTTP ${res.statusCode} from ${target.pathname}: ${rawText.slice(0, 200)}`));
						return;
					}
					if (!rawText.trim()) {
						resolve(null);
						return;
					}
					try {
						resolve(JSON.parse(rawText));
					} catch (parseErr) {
						reject(new Error(`Invalid JSON from ${target.pathname}: ${parseErr.message}`));
					}
				});
			}
		);

		req.on("timeout", () => {
			req.destroy(new Error(`Timeout after ${timeout}ms requesting ${target.pathname}`));
		});
		req.on("error", reject);

		if (payloadBuf) {
			req.write(payloadBuf);
		}
		req.end();
	});
}

function formatUtcDateOnly(year, monthZeroBased, day) {
	const d = new Date(Date.UTC(year, monthZeroBased, day));
	return d.toISOString().slice(0, 10);
}

function buildSearchPayload(source, extra) {
	const payload = {};
	if (source.query) {
		for (const [k, v] of Object.entries(source.query)) {
			if (k !== "type") {
				payload[k] = v;
			}
		}
	}
	if (source.mediaType === "IMAGE" || source.mediaType === "VIDEO") {
		payload.type = source.mediaType;
	}
	Object.assign(payload, extra);
	return payload;
}

function sortAssets(assets, sortBy, sortDescending, randomFn = Math.random) {
	const list = [...assets];
	switch (sortBy) {
		case "random":
			for (let i = list.length - 1; i > 0; i--) {
				const j = Math.floor(randomFn() * (i + 1));
				[list[i], list[j]] = [list[j], list[i]];
			}
			break;
		case "taken":
			list.sort((a, b) => {
				const ta = a.exifInfo?.dateTimeOriginal || a.localDateTime || a.fileCreatedAt || "";
				const tb = b.exifInfo?.dateTimeOriginal || b.localDateTime || b.fileCreatedAt || "";
				return ta.localeCompare(tb);
			});
			break;
		case "created":
			list.sort((a, b) => String(a.fileCreatedAt || "").localeCompare(String(b.fileCreatedAt || "")));
			break;
		case "modified":
			list.sort((a, b) => String(a.fileModifiedAt || "").localeCompare(String(b.fileModifiedAt || "")));
			break;
		case "name":
			list.sort((a, b) => String(a.originalFileName || "").toLowerCase().localeCompare(String(b.originalFileName || "").toLowerCase()));
			break;
		default:
			break;
	}
	if (sortDescending && sortBy !== "random" && sortBy !== "none") {
		list.reverse();
	}
	return list;
}

async function fetchSourceAssets(source, options = {}) {
	if (!source || !source.url) {
		return [];
	}
	const now = options.now instanceof Date ? options.now : new Date();
	const randomFn = typeof options.randomFn === "function" ? options.randomFn : Math.random;
	const logger = options.logger || null;

	let rawAssets = [];

	if (source.mode === "anniversary") {
		const baseYear = now.getUTCFullYear();
		const baseMonth = now.getUTCMonth();
		const baseDay = now.getUTCDate();

		const startAnchor = new Date(Date.UTC(baseYear, baseMonth, baseDay - source.anniversaryDatesBack));
		const endAnchor = new Date(Date.UTC(baseYear, baseMonth, baseDay + source.anniversaryDatesForward));

		const startMonth = startAnchor.getUTCMonth();
		const startDay = startAnchor.getUTCDate();
		const endMonth = endAnchor.getUTCMonth();
		const endDay = endAnchor.getUTCDate();
		const wrapsYear = startMonth > endMonth || (startMonth === endMonth && startDay > endDay);

		for (let year = source.anniversaryStartYear; year <= source.anniversaryEndYear; year++) {
			const searchEndYear = wrapsYear ? year + 1 : year;
			const startDateStr = formatUtcDateOnly(year, startMonth, startDay);
			const endDateStr = formatUtcDateOnly(searchEndYear, endMonth, endDay);

			const body = buildSearchPayload(source, {
				size: source.querySize,
				takenAfter: `${startDateStr}T00:00:00.000Z`,
				takenBefore: `${endDateStr}T23:59:59.999Z`
			});

			try {
				const data = await requestJson(source.url, "/api/search/random", {
					method: "POST",
					apiKey: source.apiKey,
					body,
					timeout: source.timeout
				});
				if (Array.isArray(data)) {
					rawAssets.push(...data);
				}
			} catch (err) {
				if (logger && typeof logger.warn === "function") {
					logger.warn(`[MMM-MediaBackground] Anniversary fetch failed for ${year}: ${err.message}`);
				}
			}
		}
	} else if (source.mode === "random") {
		const body = buildSearchPayload(source, { size: source.querySize });
		const data = await requestJson(source.url, "/api/search/random", {
			method: "POST",
			apiKey: source.apiKey,
			body,
			timeout: source.timeout
		});
		if (Array.isArray(data)) {
			rawAssets = data;
		}
	} else if (source.mode === "metadata") {
		const body = buildSearchPayload(source, { size: source.querySize });
		const data = await requestJson(source.url, "/api/search/metadata", {
			method: "POST",
			apiKey: source.apiKey,
			body,
			timeout: source.timeout
		});
		if (data && data.assets && Array.isArray(data.assets.items)) {
			rawAssets = data.assets.items;
		}
	} else if (source.mode === "memory") {
		const cursor = new Date(Date.UTC(now.getUTCFullYear(), now.getUTCMonth(), now.getUTCDate()));
		for (let d = 0; d < source.numDaysToInclude; d++) {
			const iso = cursor.toISOString();
			try {
				const buckets = await requestJson(
					source.url,
					`/api/memories?for=${encodeURIComponent(iso)}&type=on_this_day`,
					{
						method: "GET",
						apiKey: source.apiKey,
						timeout: source.timeout
					}
				);
				if (Array.isArray(buckets)) {
					for (const bucket of buckets) {
						if (bucket && Array.isArray(bucket.assets)) {
							rawAssets.push(...bucket.assets);
						}
					}
				}
			} catch (err) {
				if (logger && typeof logger.warn === "function") {
					logger.warn(`[MMM-MediaBackground] Memory fetch failed for ${iso}: ${err.message}`);
				}
			}
			cursor.setUTCDate(cursor.getUTCDate() - 1);
		}
	}

	const validAssets = rawAssets.filter((item) => item && typeof item.id === "string" && item.id.length > 0);
	return sortAssets(validAssets, source.sortBy, source.sortDescending, randomFn);
}

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
	} catch (err) {
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
		const source = resolveSource(req);
		pipeUpstreamAsset(req, res, source, "thumbnail", logger);
	});
	expressApp.get("/mediabackground/backdrop/:id", (req, res) => {
		const source = resolveSource(req);
		pipeUpstreamAsset(req, res, source, "backdrop", logger);
	});
}

function buildDisplayPayload(identifier, asset, indexZeroBased, total, sourceIndex, source, moduleConfig) {
	const querySuffix = `?source=${sourceIndex}`;
	const exif = asset.exifInfo || {};
	return {
		identifier,
		assetId: asset.id,
		type: asset.type || "IMAGE",
		originalPath: asset.originalPath || "",
		originalFileName: asset.originalFileName || "",
		previewUrl: `/mediabackground/preview/${encodeURIComponent(asset.id)}${querySuffix}`,
		backdropUrl: `/mediabackground/backdrop/${encodeURIComponent(asset.id)}${querySuffix}`,
		index: indexZeroBased + 1,
		total,
		albumName: asset.albumName || "",
		exifInfo: {
			dateTimeOriginal: exif.dateTimeOriginal || asset.localDateTime || asset.fileCreatedAt || "",
			city: exif.city ?? null,
			state: exif.state ?? null,
			country: exif.country ?? null
		},
		infoFields: source.infoFields || moduleConfig.infoFields,
		dateFormat: source.dateFormat || moduleConfig.dateFormat,
		showMediaInfo: moduleConfig.showMediaInfo,
		showBlurredBackdrop: moduleConfig.showBlurredBackdrop,
		backgroundSize: moduleConfig.backgroundSize,
		backgroundPosition: moduleConfig.backgroundPosition,
		transitionSpeed: moduleConfig.transitionSpeed,
		infoPosition: moduleConfig.infoPosition
	};
}

function createSlideshowController({ sendNotification, logger = null, nowFn = () => new Date(), randomFn = Math.random }) {
	const sessions = new Map();

	function clearTimers(session) {
		if (session.slideTimer) {
			clearTimeout(session.slideTimer);
			session.slideTimer = null;
		}
		if (session.retryTimer) {
			clearTimeout(session.retryTimer);
			session.retryTimer = null;
		}
	}

	function scheduleNextTick(session) {
		if (session.suspended || !session.assets.length) {
			return;
		}
		if (session.slideTimer) {
			clearTimeout(session.slideTimer);
		}
		const source = session.config.sources[session.activeSourceIndex] || {};
		const delayMs = source.slideshowSpeed || session.config.slideshowSpeed || DEFAULT_SLIDESHOW_SPEED_MS;
		session.slideTimer = setTimeout(() => {
			advance(session.identifier, 1, true).catch((err) => {
				if (logger && typeof logger.error === "function") {
					logger.error(`[MMM-MediaBackground] Advance error: ${err.message}`);
				}
			});
		}, delayMs);
	}

	async function refreshPool(session, startSourceIdx) {
		const sources = session.config.sources;
		if (!sources.length) {
			session.assets = [];
			return false;
		}

		for (let offset = 0; offset < sources.length; offset++) {
			const candidateIdx = (startSourceIdx + offset) % sources.length;
			const candidateSource = sources[candidateIdx];
			try {
				const items = await fetchSourceAssets(candidateSource, {
					now: nowFn(),
					randomFn,
					logger
				});
				if (items.length > 0) {
					session.activeSourceIndex = candidateIdx;
					session.assets = items;
					session.index = 0;
					session.loadedAtMs = nowFn().getTime();
					return true;
				}
			} catch (err) {
				if (logger && typeof logger.error === "function") {
					logger.error(`[MMM-MediaBackground] Source ${candidateIdx} fetch error: ${err.message}`);
				}
			}
		}

		session.assets = [];
		session.index = 0;
		return false;
	}

	function dispatchCurrent(session) {
		if (session.suspended || !session.assets.length) {
			return;
		}
		const asset = session.assets[session.index];
		const source = session.config.sources[session.activeSourceIndex];
		const payload = buildDisplayPayload(
			session.identifier,
			asset,
			session.index,
			session.assets.length,
			session.activeSourceIndex,
			source,
			session.config
		);
		sendNotification("MEDIABACKGROUND_DISPLAY", payload);
		scheduleNextTick(session);
	}

	function scheduleEmptyRetry(session) {
		if (session.suspended) {
			return;
		}
		if (session.retryTimer) {
			clearTimeout(session.retryTimer);
		}
		session.retryTimer = setTimeout(() => {
			loadAndShow(session, session.preferredSourceIndex).catch(() => {});
		}, EMPTY_RETRY_MS);
	}

	async function loadAndShow(session, startSourceIdx) {
		if (session.loading) {
			return;
		}
		session.loading = true;
		try {
			const ok = await refreshPool(session, startSourceIdx);
			if (ok) {
				dispatchCurrent(session);
			} else {
				sendNotification("MEDIABACKGROUND_EMPTY", { identifier: session.identifier });
				scheduleEmptyRetry(session);
			}
		} finally {
			session.loading = false;
		}
	}

	async function advance(identifier, delta = 1, naturalLoop = false) {
		const session = sessions.get(identifier);
		if (!session || session.suspended) {
			return;
		}

		const expired = session.loadedAtMs > 0 && (nowFn().getTime() - session.loadedAtMs) >= POOL_MAX_AGE_MS;
		if (!session.assets.length || expired) {
			await loadAndShow(session, session.preferredSourceIndex);
			return;
		}

		const nextIndex = session.index + delta;
		if (nextIndex >= session.assets.length) {
			if (naturalLoop) {
				const nextSourceIdx = session.config.cyclicSources && session.config.sources.length > 1
					? (session.activeSourceIndex + 1) % session.config.sources.length
					: session.preferredSourceIndex;
				await loadAndShow(session, nextSourceIdx);
				return;
			}
			session.index = 0;
		} else if (nextIndex < 0) {
			session.index = session.assets.length - 1;
		} else {
			session.index = nextIndex;
		}

		dispatchCurrent(session);
	}

	return {
		async configure(identifier, rawConfig, serverModuleConfig = null) {
			const mergedRaw = mergeServerSecrets(rawConfig, serverModuleConfig);
			const config = normalizeConfig(mergedRaw, { now: nowFn() });

			let session = sessions.get(identifier);
			if (!session) {
				session = {
					identifier,
					config,
					preferredSourceIndex: config.activeSourceIndex,
					activeSourceIndex: config.activeSourceIndex,
					assets: [],
					index: 0,
					loadedAtMs: 0,
					suspended: false,
					loading: false,
					slideTimer: null,
					retryTimer: null
				};
				sessions.set(identifier, session);
				await loadAndShow(session, session.preferredSourceIndex);
				return;
			}

			clearTimers(session);
			session.config = config;
			session.preferredSourceIndex = config.activeSourceIndex;
			session.suspended = false;
			if (session.assets.length > 0 && (nowFn().getTime() - session.loadedAtMs) < POOL_MAX_AGE_MS) {
				dispatchCurrent(session);
			} else {
				await loadAndShow(session, session.preferredSourceIndex);
			}
		},

		resolveSourceForRequest(req) {
			const query = (req && req.query) || {};
			const moduleId = query.module;
			const session = (moduleId && sessions.get(moduleId)) || sessions.values().next().value;
			if (!session || !session.config.sources.length) {
				return null;
			}
			const requestedIdx = query.source !== undefined ? Number(query.source) : session.activeSourceIndex;
			const safeIdx = Number.isInteger(requestedIdx) && requestedIdx >= 0 && requestedIdx < session.config.sources.length
				? requestedIdx
				: session.activeSourceIndex;
			return session.config.sources[safeIdx] || session.config.sources[0] || null;
		},

		suspend(identifier) {
			const session = sessions.get(identifier);
			if (!session) return;
			session.suspended = true;
			clearTimers(session);
		},

		resume(identifier) {
			const session = sessions.get(identifier);
			if (!session) return;
			session.suspended = false;
			if (session.assets.length > 0) {
				dispatchCurrent(session);
			} else {
				loadAndShow(session, session.preferredSourceIndex).catch(() => {});
			}
		},

		next(identifier) {
			return advance(identifier, 1, false);
		},

		previous(identifier) {
			return advance(identifier, -1, false);
		},

		skipBrokenAsset(identifier, assetId) {
			const session = sessions.get(identifier);
			if (!session || !session.assets.length) return;
			const current = session.assets[session.index];
			if (current && current.id === assetId) {
				session.assets.splice(session.index, 1);
				if (!session.assets.length) {
					loadAndShow(session, session.preferredSourceIndex).catch(() => {});
					return;
				}
				if (session.index >= session.assets.length) {
					session.index = 0;
				}
				dispatchCurrent(session);
			}
		},

		stopAll() {
			for (const session of sessions.values()) {
				session.suspended = true;
				clearTimers(session);
			}
			sessions.clear();
		}
	};
}

module.exports = {
	normalizeConfig,
	mergeServerSecrets,
	fetchSourceAssets,
	registerMediaRoutes,
	createSlideshowController
};
