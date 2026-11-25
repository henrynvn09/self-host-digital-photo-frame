/* global Module, Log, moment, document */
"use strict";

function parseDurationMs(value, fallbackMs = 1200) {
	if (typeof value === "number" && Number.isFinite(value)) {
		return Math.max(0, value);
	}
	const str = String(value ?? "").trim().toLowerCase();
	if (!str || str === "0" || str === "0s" || str === "0ms") {
		return 0;
	}
	if (str.endsWith("ms")) {
		const n = parseFloat(str.slice(0, -2));
		return Number.isFinite(n) && n >= 0 ? n : fallbackMs;
	}
	if (str.endsWith("s")) {
		const n = parseFloat(str.slice(0, -1));
		return Number.isFinite(n) && n >= 0 ? n * 1000 : fallbackMs;
	}
	const raw = parseFloat(str);
	return Number.isFinite(raw) && raw >= 0 ? raw : fallbackMs;
}

function formatCaptureDate(isoString, dateFormat) {
	if (!isoString) {
		return "";
	}
	if (typeof moment === "function") {
		const parsed = typeof moment.parseZone === "function"
			? moment.parseZone(isoString)
			: moment(isoString);
		if (parsed && typeof parsed.isValid === "function" && parsed.isValid()) {
			return parsed.format(dateFormat || "DD-MM-YYYY dddd HH:mm");
		}
	}
	return String(isoString).slice(0, 16).replace("T", " ");
}

function formatGeoLine(exifInfo) {
	if (!exifInfo || typeof exifInfo !== "object") {
		return "";
	}
	return [exifInfo.city, exifInfo.state, exifInfo.country]
		.map((part) => (typeof part === "string" ? part.trim() : ""))
		.filter((part) => part.length > 0)
		.join(", ");
}

function formatClockDuration(seconds) {
	const total = Math.max(0, Math.round(seconds));
	const pad2 = (n) => String(n).padStart(2, "0");
	if (total >= 3600) {
		const hours = Math.floor(total / 3600);
		const mins = Math.floor((total % 3600) / 60);
		const secs = total % 60;
		return `${hours}:${pad2(mins)}:${pad2(secs)}`;
	}
	const mins = Math.floor(total / 60);
	const secs = total % 60;
	return `${mins}:${pad2(secs)}`;
}

function resolvePayloadWindowSeconds(payload, config) {
	const explicit = Number(payload.playbackWindowSeconds ?? payload.windowSeconds);
	if (Number.isFinite(explicit) && explicit > 0) {
		return Number(explicit.toFixed(3));
	}
	return 30;
}

function formatVideoBadge(durationSeconds, windowSeconds) {
	const durNum = Number(durationSeconds);
	const winNum = Number(windowSeconds);
	const roundedWin = Number.isFinite(winNum) && winNum > 0 ? Math.max(1, Math.round(winNum)) : 0;
	const roundedDur = Number.isFinite(durNum) && durNum > 0 ? Math.max(1, Math.round(durNum)) : 0;

	if (roundedDur <= 0) {
		return roundedWin > 0 ? `🎥 ${formatClockDuration(roundedWin)}` : "🎥";
	}
	if (roundedWin > 0 && durNum > winNum && roundedDur > roundedWin) {
		return `🎥 ${formatClockDuration(roundedWin)} / ${formatClockDuration(roundedDur)}`;
	}
	return `🎥 ${formatClockDuration(roundedDur)}`;
}

function buildVideoStreamUrl(payload, config, nonce) {
	const windowSec = resolvePayloadWindowSeconds(payload, config);
	const rawFps = Number(payload.videoFps ?? (config && config.videoFps) ?? 30);
	const fps = Number.isFinite(rawFps) && rawFps > 0 ? Math.min(30, Math.max(1, Math.trunc(rawFps))) : 30;
	const shortMode = String(payload.shortVideoMode ?? (config && config.shortVideoMode) ?? "advance").trim().toLowerCase();
	const loopParam = shortMode === "loop" ? "true" : "false";
	let url = `/mediabackground/stream/${encodeURIComponent(payload.assetId)}?window=${encodeURIComponent(String(windowSec))}&fps=${encodeURIComponent(String(fps))}&loop=${loopParam}&t=${encodeURIComponent(String(nonce))}`;
	if (Number.isInteger(payload.sourceIndex)) {
		url += `&source=${payload.sourceIndex}`;
	}
	return url;
}

function computeOverlayModel(payload, config) {
	const fields = Array.isArray(payload.infoFields) && payload.infoFields.length > 0
		? payload.infoFields
		: ["date", "geo", "count", "album"];

	const headerLines = [];
	if (fields.includes("count") && payload.index > 0 && payload.total > 0) {
		headerLines.push(`${payload.index} of ${payload.total}`);
	}
	if (fields.includes("album") && payload.albumName && String(payload.albumName).trim().length > 0) {
		headerLines.push(String(payload.albumName).trim());
	}

	const bodyLines = [];
	for (const field of fields) {
		if (field === "date" && payload.exifInfo && payload.exifInfo.dateTimeOriginal) {
			const dateStr = formatCaptureDate(payload.exifInfo.dateTimeOriginal, payload.dateFormat);
			if (dateStr) {
				bodyLines.push(dateStr);
			}
		} else if (field === "geo") {
			const geo = formatGeoLine(payload.exifInfo);
			if (geo) {
				bodyLines.push(geo);
			}
		}
	}

	if (String(payload.type || "").toUpperCase() === "VIDEO") {
		const windowSec = resolvePayloadWindowSeconds(payload, config);
		const badge = formatVideoBadge(payload.durationSeconds, windowSec);
		if (badge) {
			bodyLines.push(badge);
		}
	}

	return { headerLines, bodyLines };
}

const mediaBackgroundDefinition = {
	requiresVersion: "2.1.0",

	defaults: {
		slideshowSpeed: 12000,
		videoSlideshowSpeed: null,
		maxVideoDurationSeconds: null,
		shortVideoMode: "advance",
		videoFps: 30,
		videoMuted: true,
		prefetchNext: true,
		coldCacheHoldMs: 8000,
		transitionSpeed: "1.2s",
		backgroundSize: "contain",
		backgroundPosition: "center",
		showBlurredBackdrop: null,
		showMediaInfo: null,
		infoFields: null,
		dateFormat: "DD-MM-YYYY dddd HH:mm",
		infoPosition: null,
		sources: null
	},

	getStyles() {
		return ["MMM-MediaBackground.css"];
	},

	getScripts() {
		return ["moment.js"];
	},

	getDoc() {
		return this.documentRef || document;
	},

	start() {
		if (typeof Log !== "undefined" && Log && typeof Log.info === "function") {
			Log.info(`Starting module: ${this.name}`);
		}
		this.stageEl = null;
		this.infoEl = null;
		this.slotTimers = new Map();
		this.streamNonce = 0;
		this.initializedBackend = false;
	},

	getDom() {
		const doc = this.getDoc();
		const root = doc.createElement("div");
		root.className = "mmmb-root";

		const stage = doc.createElement("div");
		stage.className = "mmmb-stage";
		root.appendChild(stage);
		this.stageEl = stage;

		const info = doc.createElement("div");
		info.className = "info bottomRight";
		info.style.display = "none";
		root.appendChild(info);
		this.infoEl = info;

		if (!this.initializedBackend) {
			this.initializedBackend = true;
			this.sendSocketNotification("MEDIABACKGROUND_INIT", {
				identifier: this.identifier,
				config: this.config
			});
		}

		return root;
	},

	teardownSlide(slideEl) {
		if (!slideEl) {
			return;
		}
		const timer = this.slotTimers && this.slotTimers.get(slideEl);
		if (timer) {
			clearTimeout(timer);
			this.slotTimers.delete(slideEl);
		}

		for (const child of slideEl.children) {
			if (child.tagName === "IMG") {
				child.onload = null;
				child.onerror = null;
				child.src = "";
				if (typeof child.removeAttribute === "function") {
					child.removeAttribute("src");
				}
			} else if (child.style && child.style.backgroundImage) {
				child.style.backgroundImage = "none";
			}
		}

		if (typeof slideEl.remove === "function") {
			slideEl.remove();
		} else if (slideEl.parentNode) {
			slideEl.parentNode.removeChild(slideEl);
		}
	},

	teardownAllSlides() {
		if (!this.stageEl) {
			return;
		}
		for (const slide of [...this.stageEl.children]) {
			this.teardownSlide(slide);
		}
	},

	renderOverlay(payload) {
		if (!this.infoEl) {
			return;
		}
		const pos = payload.infoPosition || "bottomRight";
		this.infoEl.className = `info ${pos}`;

		if (payload.showMediaInfo === false) {
			this.infoEl.style.display = "none";
			this.infoEl.textContent = "";
			return;
		}

		const doc = this.getDoc();
		const { headerLines, bodyLines } = computeOverlayModel(payload, this.config);
		this.infoEl.textContent = "";

		if (headerLines.length === 0 && bodyLines.length === 0) {
			this.infoEl.style.display = "none";
			return;
		}

		if (headerLines.length > 0) {
			const header = doc.createElement("header");
			header.className = "infoDivHeader";
			headerLines.forEach((line, idx) => {
				if (idx > 0) {
					header.appendChild(doc.createElement("br"));
				}
				header.appendChild(doc.createTextNode(line));
			});
			this.infoEl.appendChild(header);
		}

		for (const line of bodyLines) {
			const row = doc.createElement("div");
			row.className = "infoLine";
			row.textContent = line;
			this.infoEl.appendChild(row);
		}

		this.infoEl.style.display = "";
	},

	renderSlide(payload) {
		if (!this.stageEl) {
			return;
		}
		const doc = this.getDoc();

		// Enforce strict <= 2-slot cap BEFORE inserting the incoming slide
		while (this.stageEl.children.length >= 2) {
			this.teardownSlide(this.stageEl.children[0]);
		}

		const transitionSpeed = payload.transitionSpeed || "1.2s";
		const durationMs = parseDurationMs(transitionSpeed, 1200);
		const showBackdrop = payload.showBlurredBackdrop !== false;
		const backgroundSize = payload.backgroundSize || "contain";
		const backgroundPosition = payload.backgroundPosition || "center";
		const isVideo = String(payload.type || "").toUpperCase() === "VIDEO";

		const slide = doc.createElement("div");
		slide.className = "mmmb-slide";
		slide.style.backgroundColor = "#000";
		slide.style.opacity = "0";
		if (durationMs > 0) {
			slide.style.transition = `opacity ${transitionSpeed} ease-in-out`;
		}

		if (showBackdrop && payload.backdropUrl) {
			const backdrop = doc.createElement("div");
			backdrop.className = "mmmb-backdrop";
			backdrop.style.backgroundImage = `url("${payload.backdropUrl}")`;
			backdrop.style.backgroundSize = "cover";
			backdrop.style.backgroundPosition = "center";
			slide.appendChild(backdrop);
		}

		const img = doc.createElement("img");
		img.className = "mmmb-foreground";
		img.decoding = "async";
		img.style.objectFit = backgroundSize;
		img.style.objectPosition = backgroundPosition;
		slide.appendChild(img);

		let finalized = false;
		const finalizeCrossfade = () => {
			if (finalized) {
				return;
			}
			finalized = true;
			const timer = this.slotTimers.get(slide);
			if (timer) {
				clearTimeout(timer);
				this.slotTimers.delete(slide);
			}
			if (!this.stageEl || slide.parentNode !== this.stageEl) {
				return;
			}
			for (const sibling of [...this.stageEl.children]) {
				if (sibling === slide) {
					break;
				}
				this.teardownSlide(sibling);
			}
		};

		img.onload = () => {
			img.onload = null;
			if (!this.stageEl || slide.parentNode !== this.stageEl) {
				return;
			}
			this.renderOverlay(payload);
			slide.style.opacity = "1";

			if (durationMs <= 0 || this.stageEl.children.length <= 1) {
				finalizeCrossfade();
				return;
			}
			slide.addEventListener("transitionend", finalizeCrossfade);
			const fallbackTimer = setTimeout(finalizeCrossfade, durationMs + 100);
			this.slotTimers.set(slide, fallbackTimer);
		};

		img.onerror = () => {
			img.onload = null;
			img.onerror = null;
			if (!this.stageEl || slide.parentNode !== this.stageEl) {
				return;
			}
			this.teardownSlide(slide);
			if (this.stageEl.children.length === 0 && this.infoEl) {
				this.infoEl.style.display = "none";
			}
			this.sendSocketNotification("MEDIABACKGROUND_LOAD_ERROR", {
				identifier: this.identifier,
				assetId: payload.assetId
			});
		};

		this.stageEl.appendChild(slide);
		if (isVideo) {
			this.streamNonce = (this.streamNonce || 0) + 1;
			const nonce = `${Date.now()}-${this.streamNonce}`;
			img.src = payload.streamUrl || buildVideoStreamUrl(payload, this.config, nonce);
		} else {
			img.src = payload.previewUrl;
		}
	},

	suspend() {
		this.teardownAllSlides();
		this.sendSocketNotification("MEDIABACKGROUND_SUSPEND", {
			identifier: this.identifier
		});
	},

	resume() {
		this.sendSocketNotification("MEDIABACKGROUND_RESUME", {
			identifier: this.identifier
		});
	},

	socketNotificationReceived(notification, payload) {
		if (!payload || payload.identifier !== this.identifier) {
			return;
		}
		if (notification === "MEDIABACKGROUND_DISPLAY") {
			this.renderSlide(payload);
		}
	}
};

if (typeof Module !== "undefined" && Module && typeof Module.register === "function") {
	Module.register("MMM-MediaBackground", mediaBackgroundDefinition);
}

if (typeof module !== "undefined" && module.exports) {
	module.exports = mediaBackgroundDefinition;
}
