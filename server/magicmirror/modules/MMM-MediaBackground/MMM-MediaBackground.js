/* global Module, Log, moment, document */
"use strict";

const WEEKDAYS_FULL = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
const WEEKDAYS_SHORT = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];
const MONTHS_FULL = [
	"January", "February", "March", "April", "May", "June",
	"July", "August", "September", "October", "November", "December"
];
const MONTHS_SHORT = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"];

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

function formatBuiltInDate(isoString, pattern) {
	if (!isoString) {
		return "";
	}
	const d = new Date(isoString);
	if (Number.isNaN(d.getTime())) {
		return "";
	}
	const pad2 = (n) => String(n).padStart(2, "0");
	const useUtc = /Z$|[+-]00:00$/i.test(String(isoString).trim());
	const year = useUtc ? d.getUTCFullYear() : d.getFullYear();
	const month = useUtc ? d.getUTCMonth() : d.getMonth();
	const day = useUtc ? d.getUTCDate() : d.getDate();
	const weekday = useUtc ? d.getUTCDay() : d.getDay();
	const hours24 = useUtc ? d.getUTCHours() : d.getHours();
	const minutes = useUtc ? d.getUTCMinutes() : d.getMinutes();
	const seconds = useUtc ? d.getUTCSeconds() : d.getSeconds();
	const hours12 = hours24 % 12 || 12;

	return String(pattern || "DD-MM-YYYY dddd HH:mm").replace(
		/\[([^\]]*)\]|YYYY|YY|MMMM|MMM|MM|M|DD|D|dddd|ddd|HH|H|hh|h|mm|m|ss|s|A|a/g,
		(match, literal) => {
			if (literal !== undefined) return literal;
			switch (match) {
				case "YYYY": return String(year);
				case "YY": return String(year).slice(-2);
				case "MMMM": return MONTHS_FULL[month];
				case "MMM": return MONTHS_SHORT[month];
				case "MM": return pad2(month + 1);
				case "M": return String(month + 1);
				case "DD": return pad2(day);
				case "D": return String(day);
				case "dddd": return WEEKDAYS_FULL[weekday];
				case "ddd": return WEEKDAYS_SHORT[weekday];
				case "HH": return pad2(hours24);
				case "H": return String(hours24);
				case "hh": return pad2(hours12);
				case "h": return String(hours12);
				case "mm": return pad2(minutes);
				case "m": return String(minutes);
				case "ss": return pad2(seconds);
				case "s": return String(seconds);
				case "A": return hours24 < 12 ? "AM" : "PM";
				case "a": return hours24 < 12 ? "am" : "pm";
				default: return match;
			}
		}
	);
}

function formatCaptureDate(isoString, dateFormat) {
	if (!isoString) {
		return "";
	}
	if (typeof moment === "function") {
		const parsed = moment(isoString);
		if (parsed && typeof parsed.isValid === "function" && parsed.isValid()) {
			return parsed.format(dateFormat);
		}
	}
	return formatBuiltInDate(isoString, dateFormat);
}

function formatGeoLine(exifInfo) {
	if (!exifInfo || typeof exifInfo !== "object") {
		return "";
	}
	const parts = [exifInfo.city, exifInfo.state, exifInfo.country]
		.map((part) => (typeof part === "string" ? part.trim() : ""))
		.filter((part) => part.length > 0);
	return parts.join(", ");
}

function computeOverlayModel(payload, fallbackConfig) {
	const fields = Array.isArray(payload.infoFields) && payload.infoFields.length > 0
		? payload.infoFields
		: (Array.isArray(fallbackConfig.infoFields) ? fallbackConfig.infoFields : ["date", "geo", "count", "album"]);
	const dateFormat = payload.dateFormat || fallbackConfig.dateFormat || "DD-MM-YYYY dddd HH:mm";

	const headerLines = [];
	if (fields.includes("count") && payload.index > 0 && payload.total > 0) {
		headerLines.push(`${payload.index} of ${payload.total}`);
	}
	if (fields.includes("album") && payload.albumName && String(payload.albumName).trim().length > 0) {
		headerLines.push(String(payload.albumName).trim());
	}

	const bodyLines = [];
	const dateStr = payload.exifInfo && payload.exifInfo.dateTimeOriginal
		? formatCaptureDate(payload.exifInfo.dateTimeOriginal, dateFormat)
		: "";

	for (const field of fields) {
		switch (field) {
			case "date":
				if (dateStr) {
					bodyLines.push(dateStr);
				}
				break;
			case "geo": {
				const geo = formatGeoLine(payload.exifInfo);
				if (geo) {
					bodyLines.push(geo);
				}
				break;
			}
			case "since":
				if (payload.exifInfo && payload.exifInfo.dateTimeOriginal && typeof moment === "function") {
					const m = moment(payload.exifInfo.dateTimeOriginal);
					if (m.isValid()) {
						bodyLines.push(m.fromNow());
					}
				}
				break;
			case "name": {
				const name = payload.originalFileName || (payload.originalPath ? payload.originalPath.split("/").pop() : "");
				if (name) {
					bodyLines.push(name);
				}
				break;
			}
			default:
				break;
		}
	}

	return { headerLines, bodyLines };
}

const mediaBackgroundDefinition = {
	requiresVersion: "2.1.0",

	defaults: {
		slideshowSpeed: 12000,
		transitionSpeed: "1.2s",
		backgroundSize: "contain",
		backgroundPosition: "center",
		showBlurredBackdrop: true,
		showMediaInfo: true,
		infoFields: ["date", "geo", "count", "album"],
		dateFormat: "DD-MM-YYYY dddd HH:mm",
		infoPosition: "bottomRight",
		cyclicSources: false,
		sources: []
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

		const showInfo = this.config.showMediaInfo !== undefined
			? Boolean(this.config.showMediaInfo)
			: (this.config.showImageInfo !== undefined ? Boolean(this.config.showImageInfo) : true);

		if (showInfo) {
			const pos = this.config.infoPosition || this.config.imageInfoLocation || "bottomRight";
			const info = doc.createElement("div");
			info.className = `info ${pos}`;
			info.style.display = "none";
			root.appendChild(info);
			this.infoEl = info;
		} else {
			this.infoEl = null;
		}

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
		const existing = [...this.stageEl.children];
		for (const slide of existing) {
			this.teardownSlide(slide);
		}
	},

	renderOverlay(payload) {
		if (!this.infoEl) {
			return;
		}
		const showInfo = payload.showMediaInfo !== undefined
			? Boolean(payload.showMediaInfo)
			: true;
		if (!showInfo) {
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

		const transitionSpeed = payload.transitionSpeed ?? this.config.transitionSpeed ?? "1.2s";
		const durationMs = parseDurationMs(transitionSpeed, 1200);
		const showBackdrop = payload.showBlurredBackdrop !== undefined
			? Boolean(payload.showBlurredBackdrop)
			: (this.config.showBlurredBackdrop !== undefined
				? Boolean(this.config.showBlurredBackdrop)
				: Boolean(this.config.showBlurredImageForBlackBars ?? true));
		const backgroundSize = payload.backgroundSize || this.config.backgroundSize || "contain";
		const backgroundPosition = payload.backgroundPosition || this.config.backgroundPosition || "center";

		const slide = doc.createElement("div");
		slide.className = "mmmb-slide";
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
			// Tear down every older sibling slide ahead of this slide
			for (const sibling of [...this.stageEl.children]) {
				if (sibling === slide) {
					break;
				}
				this.teardownSlide(sibling);
			}
		};

		img.onload = () => {
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
			this.teardownSlide(slide);
			this.sendSocketNotification("MEDIABACKGROUND_LOAD_ERROR", {
				identifier: this.identifier,
				assetId: payload.assetId
			});
		};

		this.stageEl.appendChild(slide);
		img.src = payload.previewUrl;
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

	notificationReceived(notification) {
		switch (notification) {
			case "MEDIABACKGROUND_NEXT":
			case "IMMICHSLIDESHOW_NEXT":
				this.sendSocketNotification("MEDIABACKGROUND_NEXT", { identifier: this.identifier });
				break;
			case "MEDIABACKGROUND_PREVIOUS":
			case "IMMICHSLIDESHOW_PREVIOUS":
				this.sendSocketNotification("MEDIABACKGROUND_PREVIOUS", { identifier: this.identifier });
				break;
			case "MEDIABACKGROUND_PAUSE":
			case "IMMICHSLIDESHOW_PAUSE":
				this.suspend();
				break;
			case "MEDIABACKGROUND_RESUME":
			case "IMMICHSLIDESHOW_RESUME":
				this.resume();
				break;
			default:
				break;
		}
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
