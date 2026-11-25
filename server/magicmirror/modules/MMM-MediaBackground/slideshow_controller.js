"use strict";

const http = require("node:http");
const https = require("node:https");

const DEFAULT_SLIDESHOW_SPEED_MS = 12000;
const DEFAULT_VIDEO_SLIDESHOW_SPEED_MS = 30000;
const DEFAULT_COLD_CACHE_HOLD_MS = 8000;
const MAX_COLD_CACHE_HOLD_MS = 120000;
const DEFAULT_WARM_TIMEOUT_MS = 65000;
const DEFAULT_VIDEO_FPS = 30;
const MAX_VIDEO_FPS = 30;
const MIN_VIDEO_WINDOW_MS = 500;
const MAX_VIDEO_WINDOW_MS = 300000;
const DEFAULT_SHORT_VIDEO_MODE = "advance";
const VALID_SHORT_VIDEO_MODES = new Set(["advance", "loop"]);
const DEFAULT_TRANSITION_SPEED = "1.2s";
const DEFAULT_DATE_FORMAT = "DD-MM-YYYY dddd HH:mm";
const DEFAULT_INFO_FIELDS = ["date", "geo", "count", "album"];
const DEFAULT_TIMEOUT_MS = 15000;
const POOL_MAX_AGE_MS = 24 * 60 * 60 * 1000;
const EMPTY_RETRY_MS = 60 * 1000;

const VALID_MODES = new Set(["anniversary", "random", "metadata", "memory"]);
const VALID_INFO_FIELDS = new Set(["date", "geo", "count", "album"]);
const VALID_POSITIONS = new Set(["bottomRight", "bottomLeft", "topRight", "topLeft"]);

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
	if (value === undefined || value === null || value === "") {
		return fallback;
	}
	const num = Number(value);
	if (!Number.isFinite(num)) {
		return fallback;
	}
	const intVal = Math.trunc(num);
	return intVal >= min && intVal <= max ? intVal : fallback;
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

function parsePositiveNumber(value) {
	if (value === undefined || value === null || value === "") {
		return null;
	}
	const num = Number(value);
	return Number.isFinite(num) && num > 0 ? num : null;
}

function clampVideoWindowMs(ms) {
	return Math.min(MAX_VIDEO_WINDOW_MS, Math.max(MIN_VIDEO_WINDOW_MS, Math.round(ms)));
}

function resolveVideoWindowMs(rawObj, fallbackMs) {
	const maxSec = parsePositiveNumber(rawObj && rawObj.maxVideoDurationSeconds);
	if (maxSec !== null) {
		return clampVideoWindowMs(maxSec * 1000);
	}
	const speedMs = parsePositiveNumber(rawObj && rawObj.videoSlideshowSpeed);
	if (speedMs !== null) {
		return clampVideoWindowMs(speedMs);
	}
	return clampVideoWindowMs(fallbackMs);
}

function normalizeVideoFps(value) {
	const parsed = parsePositiveNumber(value);
	if (parsed === null) {
		return DEFAULT_VIDEO_FPS;
	}
	return Math.min(MAX_VIDEO_FPS, Math.max(1, Math.trunc(parsed)));
}

function normalizeShortVideoMode(value) {
	const raw = String(value ?? "").trim().toLowerCase();
	return VALID_SHORT_VIDEO_MODES.has(raw) ? raw : DEFAULT_SHORT_VIDEO_MODE;
}

function resolveEffectiveVideoDelayMs(asset, windowMs, shortVideoMode) {
	if (shortVideoMode === "advance") {
		const durationSec = parseAssetDurationSeconds(asset);
		if (durationSec > 0 && durationSec * 1000 < windowMs) {
			return Math.max(MIN_VIDEO_WINDOW_MS, Math.round(durationSec * 1000));
		}
	}
	return windowMs;
}

function normalizeMediaType(source) {
	const raw = source.mediaType ?? (source.query && source.query.type) ?? "ALL";
	const upper = String(raw).trim().toUpperCase();
	return upper === "IMAGE" || upper === "VIDEO" ? upper : "ALL";
}

function normalizeSource(rawSource, baseSource, moduleDefaults, currentYear) {
	const merged = Object.assign({}, baseSource || {}, rawSource || {});
	const rawUrl = String(merged.url ?? merged.immichUrl ?? "").trim().replace(/\/+$/, "");
	const rawMode = String(merged.mode ?? "anniversary").trim().toLowerCase();
	const mode = VALID_MODES.has(rawMode) ? rawMode : "anniversary";

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
	const slideshowSpeed = clampInt(merged.slideshowSpeed, 1000, 86400000, moduleDefaults.slideshowSpeed);
	const videoWindowMs = resolveVideoWindowMs(merged, moduleDefaults.videoWindowMs);

	return {
		url: rawUrl,
		apiKey: String(merged.apiKey ?? ""),
		mode,
		timeout: clampInt(merged.timeout ?? merged.immichTimeout, 1000, 120000, DEFAULT_TIMEOUT_MS),
		anniversaryDatesBack: nonNegativeInt(merged.anniversaryDatesBack, 45),
		anniversaryDatesForward: nonNegativeInt(merged.anniversaryDatesForward, 45),
		anniversaryStartYear: startYear,
		anniversaryEndYear: endYear,
		querySize,
		query: rawQuery,
		mediaType: normalizeMediaType(merged),
		sortBy: sortBy === "random" ? "random" : "none",
		slideshowSpeed,
		videoWindowMs,
		dateFormat: String(merged.dateFormat || moduleDefaults.dateFormat),
		infoFields: rawFields != null ? normalizeInfoFields(rawFields, moduleDefaults.infoFields) : [...moduleDefaults.infoFields]
	};
}

function normalizeConfig(rawConfig = {}, currentYear = new Date().getUTCFullYear()) {
	const slideshowSpeed = clampInt(rawConfig.slideshowSpeed, 1000, 86400000, DEFAULT_SLIDESHOW_SPEED_MS);
	const videoWindowMs = resolveVideoWindowMs(rawConfig, DEFAULT_VIDEO_SLIDESHOW_SPEED_MS);
	const shortVideoMode = normalizeShortVideoMode(rawConfig.shortVideoMode);
	const videoFps = normalizeVideoFps(rawConfig.videoFps);
	const videoMuted = rawConfig.videoMuted != null ? Boolean(rawConfig.videoMuted) : true;
	const prefetchNext = rawConfig.prefetchNext != null ? Boolean(rawConfig.prefetchNext) : true;
	const coldCacheHoldMs = clampInt(
		rawConfig.coldCacheHoldMs,
		0,
		MAX_COLD_CACHE_HOLD_MS,
		DEFAULT_COLD_CACHE_HOLD_MS
	);

	const transitionSpeed = rawConfig.transitionImages === false
		? "0s"
		: String(rawConfig.transitionSpeed ?? DEFAULT_TRANSITION_SPEED).trim() || DEFAULT_TRANSITION_SPEED;
	const backgroundSize = String(rawConfig.backgroundSize ?? "contain").trim().toLowerCase() === "cover"
		? "cover"
		: "contain";
	const backgroundPosition = String(rawConfig.backgroundPosition ?? "center").trim() || "center";

	const showBlurredBackdrop = rawConfig.showBlurredBackdrop != null
		? Boolean(rawConfig.showBlurredBackdrop)
		: (rawConfig.showBlurredImageForBlackBars != null
			? Boolean(rawConfig.showBlurredImageForBlackBars)
			: true);

	const showMediaInfo = rawConfig.showMediaInfo != null
		? Boolean(rawConfig.showMediaInfo)
		: (rawConfig.showImageInfo != null ? Boolean(rawConfig.showImageInfo) : true);

	const infoFields = normalizeInfoFields(rawConfig.infoFields ?? rawConfig.imageInfo, DEFAULT_INFO_FIELDS);
	const dateFormat = String(rawConfig.dateFormat ?? DEFAULT_DATE_FORMAT).trim() || DEFAULT_DATE_FORMAT;
	const rawPos = String(rawConfig.infoPosition ?? rawConfig.imageInfoLocation ?? "bottomRight").trim();
	const infoPosition = VALID_POSITIONS.has(rawPos) ? rawPos : "bottomRight";

	const moduleDefaults = {
		slideshowSpeed,
		videoWindowMs,
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
			sources.push(normalizeSource(entry, null, moduleDefaults, currentYear));
			inheritedBase = {
				url: entry.url ?? entry.immichUrl,
				apiKey: entry.apiKey,
				timeout: entry.timeout ?? entry.immichTimeout
			};
		} else {
			sources.push(normalizeSource(entry, inheritedBase, moduleDefaults, currentYear));
		}
	}

	return {
		slideshowSpeed,
		videoWindowMs,
		shortVideoMode,
		videoFps,
		videoMuted,
		prefetchNext,
		coldCacheHoldMs,
		transitionSpeed,
		backgroundSize,
		backgroundPosition,
		showBlurredBackdrop,
		showMediaInfo,
		infoFields,
		dateFormat,
		infoPosition,
		sources
	};
}

function mergeServerSecrets(clientConfig, serverConfig) {
	if (!serverConfig || typeof serverConfig !== "object") {
		return clientConfig || {};
	}
	const merged = Object.assign({}, serverConfig, clientConfig || {});
	const serverSources = Array.isArray(serverConfig.sources) && serverConfig.sources.length > 0
		? serverConfig.sources
		: (Array.isArray(serverConfig.immichConfigs) ? serverConfig.immichConfigs : []);

	let activeKey = null;
	if (Array.isArray(merged.sources) && merged.sources.length > 0) {
		activeKey = "sources";
	} else if (Array.isArray(merged.immichConfigs) && merged.immichConfigs.length > 0) {
		activeKey = "immichConfigs";
	}

	if (activeKey && serverSources.length > 0) {
		const primaryApiKey = serverSources[0] && serverSources[0].apiKey;
		merged[activeKey] = merged[activeKey].map((src, idx) => {
			const srvSrc = serverSources[idx] || serverSources[0] || {};
			const copy = Object.assign({}, srvSrc, src);
			if (!copy.apiKey || String(copy.apiKey).includes("**SECRET_")) {
				const restored = srvSrc.apiKey ?? primaryApiKey ?? serverConfig.apiKey;
				if (restored !== undefined) {
					copy.apiKey = restored;
				}
			}
			return copy;
		});
	}
	return merged;
}

function requestJson(baseUrl, path, { method = "POST", apiKey = "", body = null, timeout = DEFAULT_TIMEOUT_MS, signal = null } = {}) {
	return new Promise((resolve, reject) => {
		if (signal && signal.aborted) {
			reject(new Error("aborted"));
			return;
		}
		let target;
		try {
			target = new URL(path, baseUrl.endsWith("/") ? baseUrl : `${baseUrl}/`);
		} catch (err) {
			reject(err);
			return;
		}

		const transport = target.protocol === "https:" ? https : http;
		const payloadBuf = method === "GET" || body === null
			? null
			: Buffer.from(JSON.stringify(body), "utf8");
		const headers = {
			Accept: "application/json"
		};
		if (payloadBuf) {
			headers["Content-Type"] = "application/json";
			headers["Content-Length"] = String(payloadBuf.length);
		} else if (method === "POST") {
			headers["Content-Length"] = "0";
		}
		if (apiKey) {
			headers["x-api-key"] = apiKey;
		}

		let settled = false;
		const onAbort = () => {
			if (!req.destroyed) {
				req.destroy(new Error("aborted"));
			}
		};
		const finish = (fn, val) => {
			if (settled) return;
			settled = true;
			if (signal) {
				signal.removeEventListener("abort", onAbort);
			}
			fn(val);
		};

		const req = transport.request(target, { method, headers, timeout }, (res) => {
			const chunks = [];
			res.on("data", (chunk) => chunks.push(chunk));
			res.on("end", () => {
				const rawText = Buffer.concat(chunks).toString("utf8");
				if (res.statusCode < 200 || res.statusCode >= 300) {
					finish(reject, new Error(`HTTP ${res.statusCode} from ${target.pathname}: ${rawText.slice(0, 200)}`));
					return;
				}
				try {
					finish(resolve, rawText.trim() ? JSON.parse(rawText) : null);
				} catch (parseErr) {
					finish(reject, new Error(`Invalid JSON from ${target.pathname}: ${parseErr.message}`));
				}
			});
			res.on("error", (err) => finish(reject, err));
		});

		if (signal) {
			signal.addEventListener("abort", onAbort, { once: true });
		}
		req.on("timeout", () => {
			req.destroy(new Error(`Timeout after ${timeout}ms requesting ${target.pathname}`));
		});
		req.on("error", (err) => finish(reject, err));
		if (payloadBuf) {
			req.write(payloadBuf);
		}
		req.end();
	});
}

function formatUtcDateOnly(year, monthZeroBased, day) {
	return new Date(Date.UTC(year, monthZeroBased, day)).toISOString().slice(0, 10);
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
	return Object.assign(payload, extra);
}

function shuffleInPlace(list, randomFn) {
	for (let i = list.length - 1; i > 0; i--) {
		const j = Math.floor(randomFn() * (i + 1));
		[list[i], list[j]] = [list[j], list[i]];
	}
	return list;
}

function parseAssetDurationSeconds(asset) {
	if (!asset || typeof asset !== "object") {
		return 0;
	}
	if (typeof asset.durationSeconds === "number" && Number.isFinite(asset.durationSeconds) && asset.durationSeconds > 0) {
		return asset.durationSeconds;
	}
	if (typeof asset.duration === "number" && Number.isFinite(asset.duration) && asset.duration > 0) {
		return asset.duration;
	}
	if (typeof asset.duration === "string" && asset.duration.trim()) {
		const parts = asset.duration.trim().split(":").map(Number);
		if (parts.length === 3 && parts.every((n) => Number.isFinite(n) && n >= 0)) {
			return parts[0] * 3600 + parts[1] * 60 + parts[2];
		}
	}
	return 0;
}

async function fetchSourceAssets(source, { now, randomFn, logger }) {
	if (!source || !source.url) {
		return [];
	}

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
			const body = buildSearchPayload(source, {
				size: source.querySize,
				takenAfter: `${formatUtcDateOnly(year, startMonth, startDay)}T00:00:00.000Z`,
				takenBefore: `${formatUtcDateOnly(searchEndYear, endMonth, endDay)}T23:59:59.999Z`
			});

			try {
				const data = await requestJson(source.url, "/api/search/random", {
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
			apiKey: source.apiKey,
			body,
			timeout: source.timeout
		});
		if (data && data.assets && Array.isArray(data.assets.items)) {
			rawAssets = data.assets.items;
		}
	} else if (source.mode === "memory") {
		const data = await requestJson(
			source.url,
			`/api/memories?for=${encodeURIComponent(now.toISOString())}`,
			{
				method: "GET",
				apiKey: source.apiKey,
				timeout: source.timeout
			}
		);
		if (Array.isArray(data)) {
			for (const entry of data) {
				if (entry && Array.isArray(entry.assets)) {
					rawAssets.push(...entry.assets);
				}
			}
		}
		if (source.mediaType === "IMAGE" || source.mediaType === "VIDEO") {
			rawAssets = rawAssets.filter(
				(item) => item && String(item.type || "IMAGE").toUpperCase() === source.mediaType
			);
		}
	}

	const valid = rawAssets.filter((item) => item && typeof item.id === "string" && item.id.length > 0);
	return source.sortBy === "random" ? shuffleInPlace(valid, randomFn) : valid;
}

function buildDisplayPayload(session) {
	const asset = session.assets[session.index];
	const source = session.config.sources[session.activeSourceIndex];
	const querySuffix = `?source=${session.activeSourceIndex}`;
	const exif = asset.exifInfo || {};
	const isVideo = String(asset.type || "").toUpperCase() === "VIDEO";
	const windowMs = source.videoWindowMs || session.config.videoWindowMs || DEFAULT_VIDEO_SLIDESHOW_SPEED_MS;
	const playbackWindowSeconds = Number((windowMs / 1000).toFixed(3));

	return {
		identifier: session.identifier,
		assetId: asset.id,
		type: isVideo ? "VIDEO" : "IMAGE",
		durationSeconds: isVideo ? parseAssetDurationSeconds(asset) : 0,
		playbackWindowSeconds,
		shortVideoMode: session.config.shortVideoMode,
		videoFps: session.config.videoFps,
		videoMuted: session.config.videoMuted,
		sourceIndex: session.activeSourceIndex,
		previewUrl: `/mediabackground/preview/${encodeURIComponent(asset.id)}${querySuffix}`,
		backdropUrl: `/mediabackground/backdrop/${encodeURIComponent(asset.id)}${querySuffix}`,
		index: session.index + 1,
		total: session.assets.length,
		albumName: asset.albumName || "",
		exifInfo: {
			dateTimeOriginal: exif.dateTimeOriginal || asset.localDateTime || asset.fileCreatedAt || "",
			city: exif.city ?? null,
			state: exif.state ?? null,
			country: exif.country ?? null
		},
		infoFields: source.infoFields,
		dateFormat: source.dateFormat,
		showMediaInfo: session.config.showMediaInfo,
		showBlurredBackdrop: session.config.showBlurredBackdrop,
		backgroundSize: session.config.backgroundSize,
		backgroundPosition: session.config.backgroundPosition,
		transitionSpeed: session.config.transitionSpeed,
		infoPosition: session.config.infoPosition
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
		if (session.holdTimer) {
			clearTimeout(session.holdTimer);
			session.holdTimer = null;
		}
		if (session.activeWarm) {
			session.activeWarm.abort("cancelled");
			session.activeWarm = null;
		}
	}

	function startWarmOperation(session, asset) {
		const source = session.config.sources[session.activeSourceIndex];
		if (!source || !source.url || !asset || !asset.id) {
			return {
				assetId: asset ? asset.id : "",
				status: "warm",
				error: null,
				promise: Promise.resolve({ ok: true }),
				abort() {}
			};
		}

		const windowMs = source.videoWindowMs || session.config.videoWindowMs || DEFAULT_VIDEO_SLIDESHOW_SPEED_MS;
		const windowSec = Number((windowMs / 1000).toFixed(3));
		const fps = session.config.videoFps;
		const warmPath = `api/assets/${encodeURIComponent(asset.id)}/warm?window=${encodeURIComponent(String(windowSec))}&fps=${encodeURIComponent(String(fps))}`;
		const reqTimeout = Math.max(source.timeout || DEFAULT_TIMEOUT_MS, DEFAULT_WARM_TIMEOUT_MS);
		const abortController = new AbortController();

		let resolvePromise;
		const promise = new Promise((resolve) => {
			resolvePromise = resolve;
		});

		const finalize = (ok, errMsg = null) => {
			if (entry.status !== "pending") {
				return;
			}
			entry.status = ok ? "warm" : "failed";
			entry.error = errMsg;
			resolvePromise(ok ? { ok: true } : { ok: false, error: errMsg });
		};

		const entry = {
			assetId: asset.id,
			status: "pending",
			error: null,
			promise,
			abort(reason = "aborted") {
				if (entry.status !== "pending") {
					return;
				}
				abortController.abort();
				finalize(false, reason);
			}
		};

		requestJson(source.url, warmPath, {
			method: "POST",
			apiKey: source.apiKey,
			body: null,
			timeout: reqTimeout,
			signal: abortController.signal
		}).then(
			() => finalize(true, null),
			(err) => finalize(false, err ? err.message : "warm failed")
		);

		return entry;
	}

	function prefetchNextSlide(session) {
		if (session.suspended || !session.config.prefetchNext || session.index + 1 >= session.assets.length) {
			return;
		}
		const nextAsset = session.assets[session.index + 1];
		if (!nextAsset || !nextAsset.id) {
			return;
		}
		if (
			session.activeWarm &&
			session.activeWarm.assetId === nextAsset.id &&
			(session.activeWarm.status === "warm" || session.activeWarm.status === "pending")
		) {
			return;
		}
		if (session.activeWarm) {
			session.activeWarm.abort("superseded");
		}
		session.activeWarm = startWarmOperation(session, nextAsset);
	}

	function awaitCandidateWarm(session, entry, holdMs) {
		if (entry.status === "warm") {
			return Promise.resolve({ ok: true });
		}
		if (entry.status === "failed") {
			return Promise.resolve({ ok: false, error: entry.error });
		}
		if (holdMs <= 0) {
			const msg = `exceeded coldCacheHoldMs (${holdMs}ms)`;
			entry.abort(msg);
			return Promise.resolve({ ok: false, error: msg, timedOut: true });
		}

		return new Promise((resolve) => {
			let settled = false;
			const finish = (result) => {
				if (settled) {
					return;
				}
				settled = true;
				if (session.holdTimer) {
					clearTimeout(session.holdTimer);
					session.holdTimer = null;
				}
				resolve(result);
			};

			session.holdTimer = setTimeout(() => {
				session.holdTimer = null;
				const msg = `exceeded coldCacheHoldMs (${holdMs}ms)`;
				entry.abort(msg);
				finish({ ok: false, error: msg, timedOut: true });
			}, holdMs);

			entry.promise.then((res) => finish(res));
		});
	}

	function scheduleNextTick(session) {
		if (session.suspended || !session.assets.length) {
			return;
		}
		if (session.slideTimer) {
			clearTimeout(session.slideTimer);
		}
		const source = session.config.sources[session.activeSourceIndex] || {};
		const currentAsset = session.assets[session.index];
		const isVideo = Boolean(currentAsset && String(currentAsset.type || "").toUpperCase() === "VIDEO");
		const windowMs = source.videoWindowMs || session.config.videoWindowMs || DEFAULT_VIDEO_SLIDESHOW_SPEED_MS;
		const delayMs = isVideo
			? resolveEffectiveVideoDelayMs(currentAsset, windowMs, session.config.shortVideoMode)
			: (source.slideshowSpeed || session.config.slideshowSpeed || DEFAULT_SLIDESHOW_SPEED_MS);
		session.slideTimer = setTimeout(() => {
			advance(session.identifier).catch((err) => {
				if (logger && typeof logger.error === "function") {
					logger.error(`[MMM-MediaBackground] Advance error: ${err.message}`);
				}
			});
		}, delayMs);
	}

	async function refreshPool(session) {
		const sources = session.config.sources;
		for (let idx = 0; idx < sources.length; idx++) {
			try {
				const items = await fetchSourceAssets(sources[idx], {
					now: nowFn(),
					randomFn,
					logger
				});
				if (items.length > 0) {
					session.activeSourceIndex = idx;
					session.assets = items;
					session.index = 0;
					session.loadedAtMs = nowFn().getTime();
					return true;
				}
			} catch (err) {
				if (logger && typeof logger.error === "function") {
					logger.error(`[MMM-MediaBackground] Source ${idx} fetch error: ${err.message}`);
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
		sendNotification("MEDIABACKGROUND_DISPLAY", buildDisplayPayload(session));
		prefetchNextSlide(session);
		scheduleNextTick(session);
	}

	async function loadAndShow(session) {
		if (session.loading) {
			return;
		}
		session.loading = true;
		const gen = session.generation;
		try {
			const ok = await refreshPool(session);
			if (session.suspended || session.generation !== gen) {
				return;
			}
			if (ok) {
				dispatchCurrent(session);
			} else if (!session.suspended) {
				if (session.retryTimer) {
					clearTimeout(session.retryTimer);
				}
				session.retryTimer = setTimeout(() => {
					loadAndShow(session).catch(() => {});
				}, EMPTY_RETRY_MS);
			}
		} finally {
			session.loading = false;
		}
	}

	async function advance(identifier) {
		const session = sessions.get(identifier);
		if (!session || session.suspended) {
			return;
		}
		const gen = session.generation;

		const expired = session.loadedAtMs > 0 && (nowFn().getTime() - session.loadedAtMs) >= POOL_MAX_AGE_MS;
		if (!session.assets.length || expired || session.index + 1 >= session.assets.length) {
			await loadAndShow(session);
			return;
		}

		if (!session.config.prefetchNext) {
			session.index += 1;
			dispatchCurrent(session);
			return;
		}

		let remainingHoldMs = session.config.coldCacheHoldMs;
		const firstCandidateIdx = session.index + 1;

		for (let candidateIdx = firstCandidateIdx; candidateIdx < session.assets.length; candidateIdx++) {
			if (session.suspended || session.generation !== gen || !session.assets.length) {
				return;
			}
			const candidate = session.assets[candidateIdx];

			if (candidateIdx > firstCandidateIdx && remainingHoldMs <= 0) {
				session.index = candidateIdx;
				dispatchCurrent(session);
				return;
			}

			let warmEntry = session.activeWarm && session.activeWarm.assetId === candidate.id
				? session.activeWarm
				: null;
			if (!warmEntry) {
				warmEntry = startWarmOperation(session, candidate);
				session.activeWarm = warmEntry;
			}

			const startedAt = Date.now();
			const result = await awaitCandidateWarm(session, warmEntry, remainingHoldMs);
			if (session.suspended || session.generation !== gen) {
				return;
			}
			remainingHoldMs = result.timedOut
				? 0
				: Math.max(0, remainingHoldMs - Math.max(0, Date.now() - startedAt));

			if (result.ok) {
				if (session.activeWarm === warmEntry) {
					session.activeWarm = null;
				}
				session.index = candidateIdx;
				dispatchCurrent(session);
				return;
			}

			if (logger && typeof logger.warn === "function") {
				logger.warn(
					`[MMM-MediaBackground] Skipping asset ${candidate.id} after warm failure (${result.error || "failed"})`
				);
			}
			if (session.activeWarm === warmEntry) {
				session.activeWarm = null;
			}
		}

		if (!session.suspended && session.generation === gen) {
			scheduleNextTick(session);
		}
	}

	return {
		async configure(identifier, rawConfig, serverModuleConfig = null) {
			const now = nowFn();
			const mergedRaw = mergeServerSecrets(rawConfig, serverModuleConfig);
			const config = normalizeConfig(mergedRaw, now.getUTCFullYear());

			let session = sessions.get(identifier);
			if (!session) {
				session = {
					identifier,
					config,
					activeSourceIndex: 0,
					assets: [],
					index: 0,
					loadedAtMs: 0,
					suspended: false,
					loading: false,
					generation: 1,
					slideTimer: null,
					retryTimer: null,
					holdTimer: null,
					activeWarm: null
				};
				sessions.set(identifier, session);
				await loadAndShow(session);
				return;
			}

			session.generation += 1;
			clearTimers(session);
			session.config = config;
			session.suspended = false;
			await loadAndShow(session);
		},

		resolveSourceForRequest(req) {
			const query = (req && req.query) || {};
			const session = sessions.values().next().value;
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
			session.generation += 1;
			session.suspended = true;
			clearTimers(session);
		},

		resume(identifier) {
			const session = sessions.get(identifier);
			if (!session) return;
			session.generation += 1;
			session.suspended = false;
			if (session.assets.length > 0) {
				dispatchCurrent(session);
			} else {
				loadAndShow(session).catch(() => {});
			}
		},

		skipBrokenAsset(identifier, assetId) {
			const session = sessions.get(identifier);
			if (!session || session.suspended || !session.assets.length) return;
			const current = session.assets[session.index];
			if (current && current.id === assetId) {
				session.generation += 1;
				clearTimers(session);
				session.assets.splice(session.index, 1);
				if (!session.assets.length) {
					loadAndShow(session).catch(() => {});
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
				session.generation += 1;
				session.suspended = true;
				clearTimers(session);
			}
			sessions.clear();
		}
	};
}

module.exports = { createSlideshowController };
