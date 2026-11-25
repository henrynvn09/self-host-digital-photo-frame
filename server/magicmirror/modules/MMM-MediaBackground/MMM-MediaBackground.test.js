"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const http = require("node:http");

const { registerMediaRoutes } = require("./media_pipe");
const { createSlideshowController } = require("./slideshow_controller");
const MediaBackgroundModule = require("./MMM-MediaBackground");

function waitUntil(pred, maxTicks = 80) {
	return (async () => {
		for (let i = 0; i < maxTicks && !pred(); i++) {
			await new Promise((r) => setImmediate(r));
		}
	})();
}

function startTestHttpServer(handler) {
	return new Promise((resolve, reject) => {
		const server = http.createServer(handler);
		server.listen(0, "127.0.0.1", () => {
			const addr = server.address();
			resolve({
				server,
				baseUrl: `http://127.0.0.1:${addr.port}`,
				close: () => new Promise((res) => server.close(() => res()))
			});
		});
		server.on("error", reject);
	});
}

function createMiniExpressApp() {
	const routes = [];
	return {
		get(pattern, handler) {
			const paramNames = [];
			const regexStr = "^" + pattern.replace(/:([^/]+)/g, (_, name) => {
				paramNames.push(name);
				return "([^/?#]+)";
			}) + "(?:\\?.*)?$";
			routes.push({ regex: new RegExp(regexStr), paramNames, handler });
		},
		handle(req, res) {
			const parsed = new URL(req.url, "http://127.0.0.1");
			for (const r of routes) {
				const m = parsed.pathname.match(r.regex);
				if (m) {
					req.params = {};
					r.paramNames.forEach((name, idx) => {
						req.params[name] = decodeURIComponent(m[idx + 1]);
					});
					req.query = Object.fromEntries(parsed.searchParams.entries());
					return r.handler(req, res);
				}
			}
			res.statusCode = 404;
			res.end("not found");
		}
	};
}

function createTimezoneAwareTestMoment() {
	const WEEKDAYS = ["Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday"];
	const pad2 = (n) => String(n).padStart(2, "0");

	function buildFormatter(dateObj) {
		return {
			isValid: () => !Number.isNaN(dateObj.getTime()),
			format: (fmt) => {
				const yyyy = String(dateObj.getUTCFullYear());
				const mm = pad2(dateObj.getUTCMonth() + 1);
				const dd = pad2(dateObj.getUTCDate());
				const dddd = WEEKDAYS[dateObj.getUTCDay()];
				const hh = pad2(dateObj.getUTCHours());
				const min = pad2(dateObj.getUTCMinutes());
				return String(fmt)
					.replace("YYYY", yyyy)
					.replace("MM", mm)
					.replace("DD", dd)
					.replace("dddd", dddd)
					.replace("HH", hh)
					.replace("mm", min);
			}
		};
	}

	// Default moment(iso) simulates a UTC-8 browser shifting timestamps by -8 hours
	const momentFn = (iso) => {
		const shifted = new Date(new Date(iso).getTime() - 8 * 3600 * 1000);
		return buildFormatter(shifted);
	};
	// moment.parseZone(iso) preserves the wall-clock zone encoded in the timestamp
	momentFn.parseZone = (iso) => buildFormatter(new Date(iso));
	return momentFn;
}

function createFakeDomEnvironment() {
	class FakeElement {
		constructor(tagName) {
			this.tagName = tagName.toUpperCase();
			this.className = "";
			this.style = {};
			this.attributes = new Map();
			this.children = [];
			this.parentNode = null;
			this._textContent = "";
			this._listeners = new Map();
			this._src = "";
			this.onload = null;
			this.onerror = null;
			this.decoding = "";
		}

		get src() {
			return this._src;
		}

		set src(val) {
			this._src = String(val);
			if (this._src) {
				this.attributes.set("src", this._src);
			}
		}

		removeAttribute(name) {
			this.attributes.delete(name);
			if (name === "src") {
				this._src = "";
			}
		}

		get textContent() {
			if (this.children.length === 0) {
				return this._textContent;
			}
			return this._textContent + this.children.map((c) => c.textContent).join("");
		}

		set textContent(val) {
			this.children = [];
			this._textContent = String(val ?? "");
		}

		appendChild(child) {
			if (child.parentNode) {
				child.parentNode.removeChild(child);
			}
			child.parentNode = this;
			this.children.push(child);
			return child;
		}

		removeChild(child) {
			const idx = this.children.indexOf(child);
			if (idx >= 0) {
				this.children.splice(idx, 1);
				child.parentNode = null;
			}
			return child;
		}

		remove() {
			if (this.parentNode) {
				this.parentNode.removeChild(this);
			}
		}

		addEventListener(type, fn) {
			if (!this._listeners.has(type)) {
				this._listeners.set(type, []);
			}
			this._listeners.get(type).push(fn);
		}

		dispatchEvent(event) {
			const list = this._listeners.get(event.type) || [];
			for (const fn of [...list]) {
				fn(event);
			}
		}
	}

	return {
		createElement(tag) {
			return new FakeElement(tag);
		},
		createTextNode(text) {
			const el = new FakeElement("#text");
			el.textContent = text;
			return el;
		}
	};
}

test("createSlideshowController handles first-party config across anniversary (with Dec->Jan wrap), random, and metadata modes", async () => {
	const received = [];
	const upstream = await startTestHttpServer((req, res) => {
		let body = "";
		req.on("data", (c) => { body += c; });
		req.on("end", () => {
			const parsed = body ? JSON.parse(body) : {};
			received.push({ url: req.url, apiKey: req.headers["x-api-key"], body: parsed });
			res.setHeader("Content-Type", "application/json");
			if (req.url === "/api/search/random") {
				const tag = parsed.takenAfter ? parsed.takenAfter.slice(0, 4) : "rand";
				res.end(JSON.stringify([{ id: `asset-${tag}`, type: "IMAGE", originalPath: `${tag}/p.jpg`, exifInfo: {} }]));
			} else if (req.url === "/api/search/metadata") {
				res.end(JSON.stringify({
					assets: { total: 1, count: 1, items: [{ id: "asset-meta", type: "IMAGE", originalPath: "m.jpg", exifInfo: {} }] }
				}));
			} else {
				res.statusCode = 404;
				res.end("{}");
			}
		});
	});

	const emitted = [];
	const dec20 = new Date(Date.UTC(2026, 11, 20, 12, 0, 0));
	const controller = createSlideshowController({
		sendNotification: (name, payload) => emitted.push({ name, payload }),
		nowFn: () => dec20,
		randomFn: () => 0.99 // deterministic identity order for shuffle
	});

	try {
		// 1. Anniversary mode (with MagicMirror defaults merged)
		const clientCfg1 = Object.assign({}, MediaBackgroundModule.defaults, {
			slideshowSpeed: 15000,
			transitionSpeed: "1.5s",
			backgroundSize: "contain",
			showBlurredBackdrop: true,
			showMediaInfo: true,
			infoFields: ["date", "geo", "count", "album"],
			dateFormat: "DD-MM-YYYY dddd HH:mm",
			sources: [
				{
					url: `${upstream.baseUrl}/`,
					apiKey: "first-party-secret",
					mode: "anniversary",
					anniversaryDatesBack: 15,
					anniversaryDatesForward: 20,
					anniversaryStartYear: 2022,
					anniversaryEndYear: 2023,
					querySize: 50,
					sortBy: "none"
				}
			]
		});
		await controller.configure("mod_1", clientCfg1);

		assert.equal(received[0].url, "/api/search/random");
		assert.equal(received[0].apiKey, "first-party-secret");
		assert.equal(received[0].body.takenAfter, "2022-12-05T00:00:00.000Z");
		assert.equal(received[0].body.takenBefore, "2023-01-09T23:59:59.999Z");
		assert.equal(received[0].body.size, 50);
		assert.equal(received[0].body.type, undefined);
		assert.equal(emitted[0].payload.assetId, "asset-2022");
		assert.equal(emitted[0].payload.total, 2);
		assert.equal(emitted[0].payload.transitionSpeed, "1.5s");
		assert.equal(emitted[0].payload.backgroundSize, "contain");

		// 2. Metadata mode
		await controller.configure("mod_1", Object.assign({}, MediaBackgroundModule.defaults, {
			sources: [{
				url: upstream.baseUrl,
				apiKey: "first-party-secret",
				mode: "metadata",
				querySize: 10,
				mediaType: "IMAGE",
				query: { takenAfter: "2024-01-01T00:00:00Z" }
			}]
		}));
		assert.equal(received[2].url, "/api/search/metadata");
		assert.equal(received[2].body.type, "IMAGE");
		assert.equal(received[2].body.takenAfter, "2024-01-01T00:00:00Z");
		assert.equal(emitted[1].payload.assetId, "asset-meta");
	} finally {
		controller.stopAll();
		await upstream.close();
	}
});

test("createSlideshowController preserves legacy immichConfigs, showImageInfo:false, imageInfo, showBlurredImageForBlackBars:false, and redacted **SECRET_* apiKeys even after MagicMirror defaults merge", async () => {
	const upstreamKeys = [];
	const upstream = await startTestHttpServer((req, res) => {
		let body = "";
		req.on("data", (c) => { body += c; });
		req.on("end", () => {
			upstreamKeys.push(req.headers["x-api-key"]);
			const parsed = body ? JSON.parse(body) : {};
			res.setHeader("Content-Type", "application/json");
			// First source (anniversary) empty -> falls back to second legacy source (random) inheriting url & apiKey
			if (parsed.takenAfter) {
				res.end(JSON.stringify([]));
			} else {
				res.end(JSON.stringify([
					{ id: "broken-1", type: "IMAGE", originalPath: "a.jpg", exifInfo: {} },
					{ id: "legacy-ok", type: "IMAGE", originalPath: "b.jpg", albumName: "Hawaii", exifInfo: {} }
				]));
			}
		});
	});

	const emitted = [];
	const controller = createSlideshowController({
		sendNotification: (name, payload) => emitted.push({ name, payload })
	});

	try {
		const rawUserConfig = {
			slideshowSpeed: 30000,
			backgroundSize: "cover",
			showImageInfo: false,
			showBlurredImageForBlackBars: false,
			imageInfo: "date, geo",
			immichConfigs: [
				{
					apiKey: "**SECRET_0**",
					url: upstream.baseUrl,
					mode: "anniversary",
					anniversaryStartYear: 2023,
					anniversaryEndYear: 2023
				},
				{
					mode: "random",
					query: { size: 500, type: "IMAGE" }
				}
			]
		};
		const serverModuleConfig = {
			...rawUserConfig,
			immichConfigs: [
				{ ...rawUserConfig.immichConfigs[0], apiKey: "restored-legacy-secret" },
				rawUserConfig.immichConfigs[1]
			]
		};

		// Simulate exact MagicMirror client config after Module.defaults merge
		const clientMergedWithDefaults = Object.assign({}, MediaBackgroundModule.defaults, rawUserConfig);
		await controller.configure("mod_legacy", clientMergedWithDefaults, serverModuleConfig);

		assert.deepEqual(upstreamKeys, ["restored-legacy-secret", "restored-legacy-secret"]);
		assert.equal(emitted[0].payload.assetId, "broken-1");
		assert.equal(emitted[0].payload.previewUrl, "/mediabackground/preview/broken-1?source=1");
		assert.equal(emitted[0].payload.showMediaInfo, false);
		assert.equal(emitted[0].payload.showBlurredBackdrop, false);
		assert.equal(emitted[0].payload.backgroundSize, "cover");
		assert.deepEqual(emitted[0].payload.infoFields, ["date", "geo"]);

		// Broken slide skips cleanly to next asset
		controller.skipBrokenAsset("mod_legacy", "broken-1");
		assert.equal(emitted[1].payload.assetId, "legacy-ok");
	} finally {
		controller.stopAll();
		await upstream.close();
	}
});

test("Express routes /mediabackground/preview/:id and /mediabackground/backdrop/:id pipe bytes, inject x-api-key, and abort upstream on client disconnect", async () => {
	let lastAuthHeader = null;
	let upstreamAborted = false;
	let releaseUpstreamStream = null;

	const upstream = await startTestHttpServer((req, res) => {
		lastAuthHeader = req.headers["x-api-key"];
		if (req.url === "/api/assets/abc-123/thumbnail") {
			res.writeHead(200, {
				"Content-Type": "image/jpeg",
				ETag: "\"abc-123-1\"",
				"Cache-Control": "public, max-age=86400, immutable"
			});
			res.end(Buffer.from([0xff, 0xd8, 0x01, 0x02, 0xff, 0xd9]));
			return;
		}
		if (req.url === "/api/assets/abc-123/backdrop") {
			res.writeHead(200, { "Content-Type": "image/jpeg" });
			res.write(Buffer.from([0xff, 0xd8, 0xaa]));
			req.on("close", () => {
				if (!res.writableEnded) {
					upstreamAborted = true;
				}
			});
			releaseUpstreamStream = () => res.end();
			return;
		}
		res.statusCode = 404;
		res.end("missing");
	});

	const app = createMiniExpressApp();
	registerMediaRoutes(app, () => ({
		url: upstream.baseUrl,
		apiKey: "injected-server-secret",
		timeout: 5000
	}));
	const proxy = await startTestHttpServer((req, res) => app.handle(req, res));

	try {
		const previewResp = await fetch(`${proxy.baseUrl}/mediabackground/preview/abc-123`);
		assert.equal(previewResp.status, 200);
		assert.equal(previewResp.headers.get("content-type"), "image/jpeg");
		assert.equal(previewResp.headers.get("etag"), "\"abc-123-1\"");
		const previewBuf = Buffer.from(await previewResp.arrayBuffer());
		assert.deepEqual(previewBuf, Buffer.from([0xff, 0xd8, 0x01, 0x02, 0xff, 0xd9]));
		assert.equal(lastAuthHeader, "injected-server-secret");

		const badIdResp = await fetch(`${proxy.baseUrl}/mediabackground/preview/..%2Fadmin`);
		assert.equal(badIdResp.status, 400);

		await new Promise((resolve, reject) => {
			const clientReq = http.get(`${proxy.baseUrl}/mediabackground/backdrop/abc-123`, (clientRes) => {
				assert.equal(clientRes.statusCode, 200);
				clientRes.once("data", (chunk) => {
					assert.deepEqual(chunk, Buffer.from([0xff, 0xd8, 0xaa]));
					clientReq.destroy();
					resolve();
				});
			});
			clientReq.on("error", (err) => {
				if (err.code !== "ECONNRESET") reject(err);
			});
		});

		for (let i = 0; i < 40 && !upstreamAborted; i++) {
			await new Promise((r) => setTimeout(r, 15));
		}
		assert.equal(upstreamAborted, true, "expected upstream photo-api request to abort when downstream client disconnects");
		if (releaseUpstreamStream) releaseUpstreamStream();
	} finally {
		await proxy.close();
		await upstream.close();
	}
});

test("MMM-MediaBackground CSS contains zero backdrop-filter rules and sets opaque #000 slide background", () => {
	const cssPath = path.join(__dirname, "MMM-MediaBackground.css");
	const cssText = fs.readFileSync(cssPath, "utf8");
	assert.equal(/backdrop-filter/i.test(cssText), false, "MMM-MediaBackground.css must not use CSS backdrop-filter");
	assert.equal(/\.mmmb-slide\s*\{[^}]*background-color:\s*#000/m.test(cssText), true);
});

test("MMM-MediaBackground browser DOM maintains <= 2 crossfade slots, clears img.src on teardown/suspend, and preserves EXIF capture time via moment.parseZone", () => {
	const prevMoment = global.moment;
	global.moment = createTimezoneAwareTestMoment();

	try {
		const fakeDoc = createFakeDomEnvironment();
		const sentSocket = [];

		const instance = Object.create(MediaBackgroundModule);
		instance.name = "MMM-MediaBackground";
		instance.identifier = "module_1_MMM-MediaBackground";
		instance.documentRef = fakeDoc;
		instance.config = Object.assign({}, MediaBackgroundModule.defaults, {
			sources: [{ url: "http://photo-api:2283", apiKey: "**SECRET_0**" }]
		});
		instance.sendSocketNotification = (name, payload) => sentSocket.push({ name, payload });

		instance.start();
		const root = instance.getDom();

		const stage = root.children[0];
		const infoDiv = root.children[1];
		assert.equal(stage.className, "mmmb-stage");
		assert.equal(infoDiv.style.display, "none");

		// Slide 1 with backdrop enabled + EXIF UTC timestamp that would become 02:30 if shifted by UTC-8
		instance.socketNotificationReceived("MEDIABACKGROUND_DISPLAY", {
			identifier: instance.identifier,
			assetId: "photo-1",
			type: "IMAGE",
			previewUrl: "/mediabackground/preview/photo-1?source=0",
			backdropUrl: "/mediabackground/backdrop/photo-1?source=0",
			index: 3,
			total: 42,
			albumName: "Saigon Trip",
			exifInfo: {
				dateTimeOriginal: "2023-07-15T10:30:00Z",
				city: "Ho Chi Minh City",
				state: null,
				country: "Vietnam"
			},
			infoFields: ["date", "geo", "count", "album"],
			dateFormat: "DD-MM-YYYY dddd HH:mm",
			showMediaInfo: true,
			showBlurredBackdrop: true,
			backgroundSize: "contain",
			backgroundPosition: "center",
			transitionSpeed: "500ms",
			infoPosition: "bottomRight"
		});

		assert.equal(stage.children.length, 1);
		const slot1 = stage.children[0];
		const backdrop1 = slot1.children[0];
		const img1 = slot1.children[1];
		assert.equal(slot1.style.backgroundColor, "#000");
		assert.equal(backdrop1.className, "mmmb-backdrop");
		assert.equal(backdrop1.style.backgroundImage, "url(\"/mediabackground/backdrop/photo-1?source=0\")");
		assert.equal(backdrop1.style.backgroundSize, "cover");
		assert.equal(img1.tagName, "IMG");
		assert.equal(img1.style.objectFit, "contain");
		assert.equal(img1.src, "/mediabackground/preview/photo-1?source=0");

		img1.onload();
		assert.equal(slot1.style.opacity, "1");
		assert.equal(infoDiv.children[0].tagName, "HEADER");
		assert.equal(infoDiv.children[0].className, "infoDivHeader");
		assert.equal(infoDiv.children[0].textContent, "3 of 42Saigon Trip");
		assert.equal(infoDiv.children[1].textContent, "15-07-2023 Saturday 10:30");
		assert.equal(infoDiv.children[2].textContent, "Ho Chi Minh City, Vietnam");

		// Slide 2 with showBlurredBackdrop: false -> opaque #000 slide with only foreground <img>
		instance.socketNotificationReceived("MEDIABACKGROUND_DISPLAY", {
			identifier: instance.identifier,
			assetId: "photo-2",
			type: "IMAGE",
			previewUrl: "/mediabackground/preview/photo-2?source=0",
			backdropUrl: "/mediabackground/backdrop/photo-2?source=0",
			index: 4,
			total: 42,
			albumName: "",
			exifInfo: {
				dateTimeOriginal: "2022-07-15T16:00:00Z",
				city: "Los Angeles",
				state: "California",
				country: "United States"
			},
			infoFields: ["date", "geo", "count", "album"],
			dateFormat: "DD-MM-YYYY dddd HH:mm",
			showMediaInfo: true,
			showBlurredBackdrop: false,
			backgroundSize: "contain",
			backgroundPosition: "center",
			transitionSpeed: "500ms",
			infoPosition: "bottomRight"
		});

		assert.equal(stage.children.length, 2);
		const slot2 = stage.children[1];
		assert.equal(slot2.children.length, 1, "showBlurredBackdrop:false renders only foreground <img>");
		assert.equal(slot2.style.backgroundColor, "#000");
		const img2 = slot2.children[0];
		img2.onload();

		// Slide 3 arrives BEFORE Slide 2 transitionend -> oldest slot1 evicted immediately so stage stays <= 2 slots
		instance.socketNotificationReceived("MEDIABACKGROUND_DISPLAY", {
			identifier: instance.identifier,
			assetId: "photo-3",
			type: "IMAGE",
			previewUrl: "/mediabackground/preview/photo-3?source=0",
			backdropUrl: "/mediabackground/backdrop/photo-3?source=0",
			index: 5,
			total: 42,
			albumName: "Family Reunion",
			exifInfo: {
				dateTimeOriginal: "2021-12-25T08:05:00Z",
				city: null,
				state: null,
				country: null
			},
			infoFields: ["date", "geo", "count", "album"],
			dateFormat: "DD-MM-YYYY dddd HH:mm",
			showMediaInfo: true,
			showBlurredBackdrop: true,
			backgroundSize: "contain",
			backgroundPosition: "center",
			transitionSpeed: "500ms",
			infoPosition: "bottomRight"
		});

		assert.equal(stage.children.length, 2);
		assert.equal(img1.src, "");

		const slot3 = stage.children[1];
		const img3 = slot3.children[1];
		img3.onload();
		slot3.dispatchEvent({ type: "transitionend" });

		assert.equal(stage.children.length, 1);
		assert.equal(img2.src, "");
		assert.equal(img3.src, "/mediabackground/preview/photo-3?source=0");

		instance.suspend();
		assert.equal(img3.src, "");
		assert.equal(stage.children.length, 0);
	} finally {
		if (prevMoment === undefined) {
			delete global.moment;
		} else {
			global.moment = prevMoment;
		}
	}
});

test("Express route /mediabackground/stream/:id pipes multipart/x-mixed-replace chunks unbuffered with Cache-Control: no-store, forwards window/fps, injects x-api-key, and aborts upstream on client disconnect", async () => {
	let capturedUrl = null;
	let capturedApiKey = null;
	let upstreamAborted = false;
	let sendSecondChunk = null;
	let finishUpstream = null;

	const upstream = await startTestHttpServer((req, res) => {
		capturedUrl = req.url;
		capturedApiKey = req.headers["x-api-key"];
		if (req.url.startsWith("/api/assets/vid-101/stream")) {
			res.writeHead(200, {
				"Content-Type": "multipart/x-mixed-replace; boundary=frame",
				"Cache-Control": "no-store"
			});
			res.write(Buffer.from("--frame\r\nContent-Type: image/jpeg\r\nContent-Length: 4\r\n\r\n\xff\xd8\x01\xd9\r\n", "latin1"));
			sendSecondChunk = () => {
				res.write(Buffer.from("--frame\r\nContent-Type: image/jpeg\r\nContent-Length: 4\r\n\r\n\xff\xd8\x02\xd9\r\n", "latin1"));
			};
			finishUpstream = () => {
				if (!res.writableEnded) {
					res.end();
				}
			};
			req.on("close", () => {
				if (!res.writableEnded) {
					upstreamAborted = true;
				}
			});
			return;
		}
		res.statusCode = 404;
		res.end("missing");
	});

	const app = createMiniExpressApp();
	registerMediaRoutes(app, () => ({
		url: upstream.baseUrl,
		apiKey: "stream-secret",
		timeout: 5000
	}));
	const proxy = await startTestHttpServer((req, res) => app.handle(req, res));

	try {
		const badResp = await fetch(`${proxy.baseUrl}/mediabackground/stream/..%2Fsecret?window=12&fps=30`);
		assert.equal(badResp.status, 400);

		const receivedChunks = [];
		await new Promise((resolve, reject) => {
			const clientReq = http.get(
				`${proxy.baseUrl}/mediabackground/stream/vid-101?window=12&fps=24&t=999&source=0`,
				(clientRes) => {
					assert.equal(clientRes.statusCode, 200);
					assert.equal(clientRes.headers["content-type"], "multipart/x-mixed-replace; boundary=frame");
					assert.equal(clientRes.headers["cache-control"], "no-store");

					clientRes.on("data", (chunk) => {
						receivedChunks.push(Buffer.from(chunk));
						if (receivedChunks.length === 1) {
							// Upstream has NOT ended; receiving chunk 1 proves headers & chunk 1 flushed without whole-body buffering
							sendSecondChunk();
						} else if (receivedChunks.length >= 2) {
							clientReq.destroy();
							resolve();
						}
					});
				}
			);
			clientReq.on("error", (err) => {
				if (err.code !== "ECONNRESET") {
					reject(err);
				}
			});
		});

		assert.equal(capturedUrl, "/api/assets/vid-101/stream?window=12&fps=24");
		assert.equal(capturedApiKey, "stream-secret");
		const combined = Buffer.concat(receivedChunks).toString("latin1");
		assert.equal(combined.includes("\xff\xd8\x01\xd9"), true);
		assert.equal(combined.includes("\xff\xd8\x02\xd9"), true);

		for (let i = 0; i < 40 && !upstreamAborted; i++) {
			await new Promise((r) => setTimeout(r, 15));
		}
		assert.equal(upstreamAborted, true, "expected upstream MJPEG stream to abort immediately on client disconnect");
		if (finishUpstream) finishUpstream();
	} finally {
		await proxy.close();
		await upstream.close();
	}
});

test("createSlideshowController resolves Playback Window precedence, paces VIDEO vs IMAGE timers, and passes mediaType across anniversary, random, metadata, and memory pools", async (t) => {
	t.mock.timers.enable({ apis: ["setTimeout"] });

	const requests = [];
	const upstream = await startTestHttpServer((req, res) => {
		let raw = "";
		req.on("data", (c) => { raw += c; });
		req.on("end", () => {
			const parsed = raw ? JSON.parse(raw) : null;
			requests.push({ method: req.method, url: req.url, apiKey: req.headers["x-api-key"], body: parsed });
			res.setHeader("Content-Type", "application/json");
			if (req.url.startsWith("/api/memories")) {
				res.end(JSON.stringify([
					{
						id: "on-this-day-2022-07-15",
						type: "on_this_day",
						assets: [
							{ id: "mem-img", type: "IMAGE", durationSeconds: 0, exifInfo: {} },
							{ id: "mem-vid", type: "VIDEO", durationSeconds: 18.4, exifInfo: {} }
						]
					}
				]));
				return;
			}
			res.end(JSON.stringify([
				{ id: "vid-1", type: "VIDEO", durationSeconds: 45.2, albumName: "Summer", exifInfo: {} },
				{ id: "img-2", type: "IMAGE", durationSeconds: 0, albumName: "Summer", exifInfo: {} }
			]));
		});
	});

	const emitted = [];
	const july15 = new Date(Date.UTC(2026, 6, 15, 12, 0, 0));
	const controller = createSlideshowController({
		sendNotification: (name, payload) => emitted.push({ name, payload }),
		nowFn: () => july15,
		randomFn: () => 0.99
	});

	const flushAsyncPoolReload = async (expectedLen) => {
		for (let i = 0; i < 50 && emitted.length < expectedLen; i++) {
			await new Promise((r) => setImmediate(r));
		}
	};

	try {
		// 1. Default Playback Window falls back to slideshowSpeed (15000ms -> 15s), videoFps=30, videoMuted=true
		await controller.configure("mod_vid", Object.assign({}, MediaBackgroundModule.defaults, {
			slideshowSpeed: 15000,
			sources: [{
				url: upstream.baseUrl,
				apiKey: "vid-key",
				mode: "random",
				sortBy: "none"
			}]
		}));

		assert.equal(requests[0].body.type, undefined);
		assert.equal(emitted.length, 1);
		assert.equal(emitted[0].payload.assetId, "vid-1");
		assert.equal(emitted[0].payload.type, "VIDEO");
		assert.equal(emitted[0].payload.durationSeconds, 45.2);
		assert.equal(emitted[0].payload.playbackWindowSeconds, 15);
		assert.equal(emitted[0].payload.videoFps, 30);
		assert.equal(emitted[0].payload.videoMuted, true);

		t.mock.timers.tick(14999);
		assert.equal(emitted.length, 1, "should not advance before 15s default Playback Window");
		t.mock.timers.tick(1);
		await flushAsyncPoolReload(2);
		assert.equal(emitted.length, 2);
		assert.equal(emitted[1].payload.assetId, "img-2");
		assert.equal(emitted[1].payload.type, "IMAGE");

		// 2. Explicit videoSlideshowSpeed (20000ms) paces VIDEO at 20s while IMAGE stays at slideshowSpeed (12000ms)
		emitted.length = 0;
		await controller.configure("mod_vid", Object.assign({}, MediaBackgroundModule.defaults, {
			slideshowSpeed: 12000,
			videoSlideshowSpeed: 20000,
			sources: [{
				url: upstream.baseUrl,
				apiKey: "vid-key",
				mode: "random",
				mediaType: "VIDEO",
				sortBy: "none"
			}]
		}));
		assert.equal(requests.at(-1).body.type, "VIDEO");
		assert.equal(emitted[0].payload.playbackWindowSeconds, 20);

		t.mock.timers.tick(19999);
		assert.equal(emitted.length, 1);
		t.mock.timers.tick(1);
		await flushAsyncPoolReload(2);
		assert.equal(emitted.length, 2);
		assert.equal(emitted[1].payload.assetId, "img-2");

		// IMAGE slide advances after slideshowSpeed (12000ms), NOT videoSlideshowSpeed (20000ms)
		t.mock.timers.tick(11999);
		assert.equal(emitted.length, 2);
		t.mock.timers.tick(1);
		await flushAsyncPoolReload(3);
		assert.equal(emitted.length, 3, "IMAGE slide should trigger next tick after 12s slideshowSpeed");

		// 3. maxVideoDurationSeconds (8s) overrides videoSlideshowSpeed (20000ms), plus memory mode with mediaType="VIDEO"
		emitted.length = 0;
		await controller.configure("mod_vid", Object.assign({}, MediaBackgroundModule.defaults, {
			slideshowSpeed: 14000,
			videoSlideshowSpeed: 20000,
			maxVideoDurationSeconds: 8,
			videoFps: 24,
			videoMuted: false,
			immichConfigs: [{
				url: upstream.baseUrl,
				apiKey: "vid-key",
				mode: "memory",
				query: { type: "VIDEO" },
				sortBy: "none"
			}]
		}));

		assert.equal(requests.at(-1).method, "GET");
		assert.equal(requests.at(-1).url.startsWith("/api/memories?for=2026-07-15"), true);
		assert.equal(emitted.length, 1);
		assert.equal(emitted[0].payload.assetId, "mem-vid", "memory mode with query.type=VIDEO should filter to VIDEO assets");
		assert.equal(emitted[0].payload.playbackWindowSeconds, 8);
		assert.equal(emitted[0].payload.videoFps, 24);
		assert.equal(emitted[0].payload.videoMuted, false);

		t.mock.timers.tick(7999);
		assert.equal(emitted.length, 1);
		t.mock.timers.tick(1);
		await flushAsyncPoolReload(2);
		// Single-item pool reloads after 8s Playback Window
		assert.equal(emitted.length, 2);
	} finally {
		controller.stopAll();
		await upstream.close();
	}
});

test("MMM-MediaBackground renders VIDEO slides via <img> MJPEG stream over static backdrop, formats trimmed vs looped 🎥 badges, and clears img.src immediately on teardown and suspend", () => {
	const prevMoment = global.moment;
	global.moment = createTimezoneAwareTestMoment();

	try {
		const fakeDoc = createFakeDomEnvironment();
		const instance = Object.create(MediaBackgroundModule);
		instance.name = "MMM-MediaBackground";
		instance.identifier = "module_video_1";
		instance.documentRef = fakeDoc;
		instance.config = Object.assign({}, MediaBackgroundModule.defaults, {
			slideshowSpeed: 12000,
			sources: [{ url: "http://photo-api:2283", apiKey: "**SECRET_0**" }]
		});
		instance.sendSocketNotification = () => {};

		instance.start();
		const root = instance.getDom();
		const stage = root.children[0];
		const infoDiv = root.children[1];

		// 1. Trimmed VIDEO (45s > 12s Playback Window) -> 🎥 0:12 / 0:45
		instance.socketNotificationReceived("MEDIABACKGROUND_DISPLAY", {
			identifier: instance.identifier,
			assetId: "vid-trimmed",
			type: "VIDEO",
			durationSeconds: 45,
			playbackWindowSeconds: 12,
			videoFps: 30,
			sourceIndex: 0,
			previewUrl: "/mediabackground/preview/vid-trimmed?source=0",
			backdropUrl: "/mediabackground/backdrop/vid-trimmed?source=0",
			index: 2,
			total: 10,
			albumName: "Birthday Party",
			exifInfo: {
				dateTimeOriginal: "2023-07-15T10:30:00Z",
				city: "Irvine",
				state: "California",
				country: "United States"
			},
			infoFields: ["date", "geo", "count", "album"],
			dateFormat: "DD-MM-YYYY dddd HH:mm",
			showMediaInfo: true,
			showBlurredBackdrop: true,
			backgroundSize: "contain",
			backgroundPosition: "center",
			transitionSpeed: "500ms",
			infoPosition: "bottomRight"
		});

		assert.equal(stage.children.length, 1);
		const slot1 = stage.children[0];
		const backdrop1 = slot1.children[0];
		const img1 = slot1.children[1];
		assert.equal(backdrop1.className, "mmmb-backdrop");
		assert.equal(backdrop1.style.backgroundImage, "url(\"/mediabackground/backdrop/vid-trimmed?source=0\")");
		assert.equal(img1.tagName, "IMG");

		const parsedUrl1 = new URL(img1.src, "http://127.0.0.1");
		assert.equal(parsedUrl1.pathname, "/mediabackground/stream/vid-trimmed");
		assert.equal(parsedUrl1.searchParams.get("window"), "12");
		assert.equal(parsedUrl1.searchParams.get("fps"), "30");
		assert.equal(Boolean(parsedUrl1.searchParams.get("t")), true, "expected cache-busting t=<nonce> param");

		img1.onload();
		assert.equal(img1.onload, null, "img.onload must detach after first frame so MJPEG frames never re-trigger overlay/timer setup");
		assert.equal(infoDiv.children[0].textContent, "2 of 10Birthday Party");
		assert.equal(infoDiv.children[1].textContent, "15-07-2023 Saturday 10:30");
		assert.equal(infoDiv.children[2].textContent, "Irvine, California, United States");
		assert.equal(infoDiv.children[3].textContent, "🎥 0:12 / 0:45");

		// 2. Looped shorter VIDEO (5s <= 12s Playback Window) -> 🎥 0:05 and tears down outgoing MJPEG img1.src=""
		instance.socketNotificationReceived("MEDIABACKGROUND_DISPLAY", {
			identifier: instance.identifier,
			assetId: "vid-loop",
			type: "VIDEO",
			durationSeconds: 5,
			playbackWindowSeconds: 12,
			videoFps: 24,
			previewUrl: "/mediabackground/preview/vid-loop?source=0",
			backdropUrl: "/mediabackground/backdrop/vid-loop?source=0",
			index: 3,
			total: 10,
			albumName: "",
			exifInfo: {
				dateTimeOriginal: "2024-01-01T08:00:00Z",
				city: null,
				state: null,
				country: null
			},
			infoFields: ["date", "geo", "count", "album"],
			dateFormat: "DD-MM-YYYY dddd HH:mm",
			showMediaInfo: true,
			showBlurredBackdrop: true,
			backgroundSize: "contain",
			backgroundPosition: "center",
			transitionSpeed: "500ms",
			infoPosition: "bottomRight"
		});

		const slot2 = stage.children[1];
		const img2 = slot2.children[1];
		const parsedUrl2 = new URL(img2.src, "http://127.0.0.1");
		assert.equal(parsedUrl2.pathname, "/mediabackground/stream/vid-loop");
		assert.equal(parsedUrl2.searchParams.get("window"), "12");
		assert.equal(parsedUrl2.searchParams.get("fps"), "24");
		assert.notEqual(parsedUrl2.searchParams.get("t"), parsedUrl1.searchParams.get("t"));

		img2.onload();
		slot2.dispatchEvent({ type: "transitionend" });

		assert.equal(stage.children.length, 1);
		assert.equal(img1.src, "", "outgoing MJPEG img.src must be cleared immediately on slide transition teardown");
		assert.equal(infoDiv.children.at(-1).textContent, "🎥 0:05");

		// 3. Equal-length clip (12.02s ~ 12s window) renders 🎥 0:12 (not 🎥 0:12 / 0:12) and suspend() clears img.src=""
		instance.socketNotificationReceived("MEDIABACKGROUND_DISPLAY", {
			identifier: instance.identifier,
			assetId: "vid-equal",
			type: "VIDEO",
			durationSeconds: 12.02,
			playbackWindowSeconds: 12,
			videoFps: 30,
			previewUrl: "/mediabackground/preview/vid-equal",
			backdropUrl: "/mediabackground/backdrop/vid-equal",
			index: 4,
			total: 10,
			albumName: "",
			exifInfo: { dateTimeOriginal: "2024-01-01T08:00:00Z" },
			infoFields: ["date"],
			dateFormat: "DD-MM-YYYY dddd HH:mm",
			showMediaInfo: true,
			showBlurredBackdrop: true,
			transitionSpeed: "0s"
		});
		const slot3 = stage.children.at(-1);
		const img3 = slot3.children[1];
		img3.onload();
		assert.equal(img2.src, "");
		assert.equal(infoDiv.children.at(-1).textContent, "🎥 0:12");

		instance.suspend();
		assert.equal(img3.src, "", "suspend() must clear active MJPEG img.src immediately");
		assert.equal(stage.children.length, 0);
	} finally {
		if (prevMoment === undefined) {
			delete global.moment;
		} else {
			global.moment = prevMoment;
		}
	}
});

test("createSlideshowController triggers N+1 /warm prefetch immediately upon displaying Slide N, refreshes pool at end of batch, and skips prefetch when prefetchNext:false", async (t) => {
	t.mock.timers.enable({ apis: ["setTimeout"] });

	const warmRequests = [];
	let poolFetchCount = 0;
	const upstream = await startTestHttpServer((req, res) => {
		let raw = "";
		req.on("data", (c) => { raw += c; });
		req.on("end", () => {
			res.setHeader("Content-Type", "application/json");
			if (req.url.startsWith("/api/assets/") && req.url.includes("/warm")) {
				warmRequests.push({
					method: req.method,
					url: req.url,
					apiKey: req.headers["x-api-key"]
				});
				res.end(JSON.stringify({ status: "ok" }));
				return;
			}
			poolFetchCount += 1;
			res.end(JSON.stringify([
				{ id: `batch${poolFetchCount}-slide-0`, type: "IMAGE", durationSeconds: 0, exifInfo: {} },
				{ id: `batch${poolFetchCount}-slide-1`, type: "VIDEO", durationSeconds: 30, exifInfo: {} },
				{ id: `batch${poolFetchCount}-slide-2`, type: "IMAGE", durationSeconds: 0, exifInfo: {} }
			]));
		});
	});

	const emitted = [];
	const controller = createSlideshowController({
		sendNotification: (name, payload) => emitted.push({ name, payload }),
		nowFn: () => new Date(Date.UTC(2026, 6, 15, 12, 0, 0)),
		randomFn: () => 0.99
	});

	try {
		await controller.configure("mod_prefetch", Object.assign({}, MediaBackgroundModule.defaults, {
			slideshowSpeed: 10000,
			maxVideoDurationSeconds: 14,
			videoFps: 24,
			sources: [{
				url: upstream.baseUrl,
				apiKey: "warm-secret",
				mode: "random",
				sortBy: "none"
			}]
		}));

		assert.equal(emitted.length, 1);
		assert.equal(emitted[0].payload.assetId, "batch1-slide-0");

		// Immediately upon displaying Slide 0, POST /api/assets/batch1-slide-1/warm fires in background
		await waitUntil(() => warmRequests.length >= 1);
		assert.equal(warmRequests.length, 1);
		assert.equal(warmRequests[0].method, "POST");
		assert.equal(warmRequests[0].url, "/api/assets/batch1-slide-1/warm?window=14&fps=24");
		assert.equal(warmRequests[0].apiKey, "warm-secret");

		// Advance to Slide 1 (VIDEO, 14s window) -> immediately warms Slide 2
		t.mock.timers.tick(10000);
		await waitUntil(() => emitted.length >= 2 && warmRequests.length >= 2);
		assert.equal(emitted[1].payload.assetId, "batch1-slide-1");
		assert.equal(warmRequests[1].url, "/api/assets/batch1-slide-2/warm?window=14&fps=24");

		// Advance to Slide 2 (last item in pool) -> does NOT wastefully wrap-warm batch1-slide-0
		t.mock.timers.tick(14000);
		await waitUntil(() => emitted.length >= 3);
		await waitUntil(() => false, 15);
		assert.equal(emitted[2].payload.assetId, "batch1-slide-2");
		assert.equal(warmRequests.length, 2, "last slide in pool should wait for pool refresh rather than wrap-warming old batch");

		// Advance past end of pool -> refreshes pool to batch 2, displays batch2-slide-0, and prefetches batch2-slide-1
		t.mock.timers.tick(10000);
		await waitUntil(() => emitted.length >= 4 && warmRequests.length >= 3);
		assert.equal(poolFetchCount, 2);
		assert.equal(emitted[3].payload.assetId, "batch2-slide-0");
		assert.equal(warmRequests[2].url, "/api/assets/batch2-slide-1/warm?window=14&fps=24");

		// Reconfigure with prefetchNext: false -> zero /warm requests
		warmRequests.length = 0;
		emitted.length = 0;
		await controller.configure("mod_prefetch", Object.assign({}, MediaBackgroundModule.defaults, {
			slideshowSpeed: 10000,
			prefetchNext: false,
			sources: [{
				url: upstream.baseUrl,
				apiKey: "warm-secret",
				mode: "random",
				sortBy: "none"
			}]
		}));
		await waitUntil(() => false, 15);
		assert.equal(warmRequests.length, 0, "prefetchNext:false must not send /warm requests");
		t.mock.timers.tick(10000);
		await waitUntil(() => emitted.length >= 2);
		assert.equal(emitted[1].payload.assetId, "batch3-slide-1");
		assert.equal(warmRequests.length, 0);
	} finally {
		controller.stopAll();
		await upstream.close();
	}
});

test("createSlideshowController holds Slide N on screen up to coldCacheHoldMs while Slide N+1 warms and dispatches N+1 the instant /warm completes", async (t) => {
	t.mock.timers.enable({ apis: ["setTimeout"] });

	let releaseSlideBWarm = null;
	const warmSeen = [];
	const upstream = await startTestHttpServer((req, res) => {
		req.resume();
		req.on("end", () => {
			res.setHeader("Content-Type", "application/json");
			if (req.url.startsWith("/api/assets/slide-b/warm")) {
				warmSeen.push("slide-b");
				releaseSlideBWarm = () => {
					if (!res.writableEnded) {
						res.end(JSON.stringify({ status: "ok" }));
					}
				};
				return;
			}
			if (req.url.startsWith("/api/assets/") && req.url.includes("/warm")) {
				warmSeen.push(req.url);
				res.end(JSON.stringify({ status: "ok" }));
				return;
			}
			res.end(JSON.stringify([
				{ id: "slide-a", type: "IMAGE", durationSeconds: 0, exifInfo: {} },
				{ id: "slide-b", type: "VIDEO", durationSeconds: 25, exifInfo: {} },
				{ id: "slide-c", type: "IMAGE", durationSeconds: 0, exifInfo: {} }
			]));
		});
	});

	const emitted = [];
	const controller = createSlideshowController({
		sendNotification: (name, payload) => emitted.push({ name, payload }),
		nowFn: () => new Date(Date.UTC(2026, 6, 15, 12, 0, 0)),
		randomFn: () => 0.99
	});

	try {
		// Uses default coldCacheHoldMs (8000ms) from MediaBackgroundModule.defaults
		await controller.configure("mod_hold", Object.assign({}, MediaBackgroundModule.defaults, {
			slideshowSpeed: 12000,
			sources: [{
				url: upstream.baseUrl,
				apiKey: "hold-key",
				mode: "random",
				sortBy: "none"
			}]
		}));

		assert.equal(emitted.length, 1);
		assert.equal(emitted[0].payload.assetId, "slide-a");
		await waitUntil(() => releaseSlideBWarm !== null);

		// Slide A's 12s timer expires while slide-b /warm is still in flight -> holds Slide A on screen!
		t.mock.timers.tick(12000);
		await waitUntil(() => false);
		assert.equal(emitted.length, 1, "must hold Slide A on screen while Slide B is still warming");

		// 3500ms into the 8000ms grace window -> still holding Slide A cleanly
		t.mock.timers.tick(3500);
		await waitUntil(() => false);
		assert.equal(emitted.length, 1, "must keep holding Slide A within coldCacheHoldMs grace window");

		// Slide B finishes warming at +3500ms -> dispatches Slide B IMMEDIATELY without waiting remaining 4500ms!
		releaseSlideBWarm();
		await waitUntil(() => emitted.length >= 2);
		assert.equal(emitted.length, 2);
		assert.equal(emitted[1].payload.assetId, "slide-b");
	} finally {
		if (releaseSlideBWarm) releaseSlideBWarm();
		controller.stopAll();
		await upstream.close();
	}
});

test("createSlideshowController skips corrupt or timed-out Slide N+1 straight to N+2, honors explicit coldCacheHoldMs:0, and bounds all-corrupt pools by pool.length", async (t) => {
	t.mock.timers.enable({ apis: ["setTimeout"] });

	let mode = "error_n1";
	let warmAttempts = [];
	const pendingHungResponses = [];

	const upstream = await startTestHttpServer((req, res) => {
		req.resume();
		req.on("end", () => {
			res.setHeader("Content-Type", "application/json");
			if (req.url.startsWith("/api/assets/") && req.url.includes("/warm")) {
				warmAttempts.push(req.url);
				if (mode === "error_n1" && req.url.includes("/bad-1/warm")) {
					res.statusCode = 422;
					res.end(JSON.stringify({ message: "corrupt video" }));
					return;
				}
				if ((mode === "timeout_n1" || mode === "zero_hold") && (req.url.includes("/hung-1/warm") || req.url.includes("/hung-2/warm"))) {
					pendingHungResponses.push(res);
					return;
				}
				if (mode === "all_corrupt") {
					res.statusCode = 422;
					res.end(JSON.stringify({ message: "all corrupt" }));
					return;
				}
				res.end(JSON.stringify({ status: "ok" }));
				return;
			}

			if (mode === "error_n1") {
				res.end(JSON.stringify([
					{ id: "ok-0", type: "IMAGE", exifInfo: {} },
					{ id: "bad-1", type: "VIDEO", durationSeconds: 10, exifInfo: {} },
					{ id: "ok-2", type: "IMAGE", exifInfo: {} }
				]));
			} else if (mode === "timeout_n1" || mode === "zero_hold") {
				res.end(JSON.stringify([
					{ id: "ok-0", type: "IMAGE", exifInfo: {} },
					{ id: "hung-1", type: "VIDEO", durationSeconds: 10, exifInfo: {} },
					{ id: "hung-2", type: "IMAGE", exifInfo: {} }
				]));
			} else {
				res.end(JSON.stringify([
					{ id: "bad-a", type: "VIDEO", durationSeconds: 10, exifInfo: {} },
					{ id: "bad-b", type: "VIDEO", durationSeconds: 10, exifInfo: {} },
					{ id: "bad-c", type: "VIDEO", durationSeconds: 10, exifInfo: {} }
				]));
			}
		});
	});

	const emitted = [];
	const warnings = [];
	const controller = createSlideshowController({
		sendNotification: (name, payload) => emitted.push({ name, payload }),
		logger: {
			warn: (msg) => warnings.push(msg),
			error: () => {}
		},
		nowFn: () => new Date(Date.UTC(2026, 6, 15, 12, 0, 0)),
		randomFn: () => 0.99
	});

	try {
		// 1. Corrupt N+1 (422) -> keeps ok-0 on screen for full 12s, logs warning, and advances straight to ok-2 (N+2)
		await controller.configure("mod_skip", Object.assign({}, MediaBackgroundModule.defaults, {
			slideshowSpeed: 12000,
			sources: [{ url: upstream.baseUrl, apiKey: "k", mode: "random", sortBy: "none" }]
		}));
		await waitUntil(() => warmAttempts.length >= 1);
		assert.equal(emitted.length, 1);
		assert.equal(emitted[0].payload.assetId, "ok-0");

		t.mock.timers.tick(11999);
		assert.equal(emitted.length, 1, "Slide ok-0 must stay on screen for its full 12s display interval");
		t.mock.timers.tick(1);
		await waitUntil(() => emitted.length >= 2);
		assert.equal(emitted[1].payload.assetId, "ok-2", "must skip failed bad-1 straight to ok-2 (N+2)");
		assert.equal(warnings.some((w) => w.includes("bad-1")), true, "expected warning logged for skipped bad-1");

		// 2. Timed-out N+1 exceeding coldCacheHoldMs (8000ms default) -> caps total hold on ok-0 to 8000ms (even if N+2 also hangs!) and advances straight to hung-2
		mode = "timeout_n1";
		emitted.length = 0;
		warnings.length = 0;
		warmAttempts = [];
		await controller.configure("mod_skip", Object.assign({}, MediaBackgroundModule.defaults, {
			slideshowSpeed: 12000,
			sources: [{ url: upstream.baseUrl, apiKey: "k", mode: "random", sortBy: "none" }]
		}));
		await waitUntil(() => warmAttempts.length >= 1);
		assert.equal(emitted[0].payload.assetId, "ok-0");

		t.mock.timers.tick(12000);
		await waitUntil(() => false, 15);
		assert.equal(emitted.length, 1, "must hold ok-0 when hung-1 is still warming at 12s");

		t.mock.timers.tick(7999);
		await waitUntil(() => false, 15);
		assert.equal(emitted.length, 1, "must hold ok-0 until full 8000ms coldCacheHoldMs expires");

		t.mock.timers.tick(1);
		await waitUntil(() => emitted.length >= 2);
		assert.equal(emitted[1].payload.assetId, "hung-2", "must advance straight to N+2 after single coldCacheHoldMs budget expires without a second hold");
		assert.equal(warnings.some((w) => w.includes("hung-1")), true, "expected warning logged for timed-out hung-1");

		// 3. Explicit coldCacheHoldMs: 0 -> allowed (not coerced to 8000ms!), skips hung-1 immediately at 12s with 0ms hold
		mode = "zero_hold";
		emitted.length = 0;
		warnings.length = 0;
		warmAttempts = [];
		await controller.configure("mod_skip", Object.assign({}, MediaBackgroundModule.defaults, {
			slideshowSpeed: 12000,
			coldCacheHoldMs: 0,
			sources: [{ url: upstream.baseUrl, apiKey: "k", mode: "random", sortBy: "none" }]
		}));
		await waitUntil(() => warmAttempts.length >= 1);
		assert.equal(emitted[0].payload.assetId, "ok-0");

		t.mock.timers.tick(12000);
		await waitUntil(() => emitted.length >= 2);
		assert.equal(emitted[1].payload.assetId, "hung-2", "coldCacheHoldMs:0 must skip in-flight hung-1 at timer expiry without extra hold");
		assert.equal(warnings.some((w) => w.includes("hung-1")), true);

		// 4. All-corrupt pool -> checks only remaining slides (bad-b, bad-c), never re-checks bad-a or infinite-loops
		mode = "all_corrupt";
		emitted.length = 0;
		warnings.length = 0;
		warmAttempts = [];
		await controller.configure("mod_skip", Object.assign({}, MediaBackgroundModule.defaults, {
			slideshowSpeed: 12000,
			sources: [{ url: upstream.baseUrl, apiKey: "k", mode: "random", sortBy: "none" }]
		}));
		await waitUntil(() => warmAttempts.length >= 1);
		assert.equal(emitted.length, 1);

		t.mock.timers.tick(12000);
		await waitUntil(() => warnings.length >= 2);
		await waitUntil(() => false, 15);
		assert.equal(emitted.length, 1, "must not dispatch known-corrupt candidates when entire pool fails warm");
		assert.equal(warnings.length, 2, "must check only remaining slides bad-b and bad-c without re-checking current bad-a");
		assert.equal(warmAttempts.some((u) => u.includes("/bad-a/warm")), false, "must never re-warm current Slide N");
	} finally {
		for (const res of pendingHungResponses) {
			if (!res.writableEnded) res.end("{}");
		}
		controller.stopAll();
		await upstream.close();
	}
});

test("MMM-MediaBackground handles img.onerror gracefully for both pre-load failures and mid-flight MJPEG stream drops without duplicate error notifications", () => {
	const fakeDoc = createFakeDomEnvironment();
	const sentNotifications = [];

	const instance = Object.create(MediaBackgroundModule);
	instance.name = "MMM-MediaBackground";
	instance.identifier = "module_err_1";
	instance.documentRef = fakeDoc;
	instance.config = Object.assign({}, MediaBackgroundModule.defaults, {
		slideshowSpeed: 12000,
		sources: [{ url: "http://photo-api:2283", apiKey: "**SECRET_0**" }]
	});
	instance.sendSocketNotification = (name, payload) => sentNotifications.push({ name, payload });

	instance.start();
	const root = instance.getDom();
	const stage = root.children[0];
	const infoDiv = root.children[1];
	sentNotifications.length = 0;

	// 1. Load healthy Slide 1
	instance.socketNotificationReceived("MEDIABACKGROUND_DISPLAY", {
		identifier: instance.identifier,
		assetId: "photo-healthy",
		type: "IMAGE",
		previewUrl: "/mediabackground/preview/photo-healthy?source=0",
		backdropUrl: "/mediabackground/backdrop/photo-healthy?source=0",
		index: 1,
		total: 5,
		albumName: "Healthy Album",
		exifInfo: { dateTimeOriginal: "2024-05-01T10:00:00Z" },
		infoFields: ["date", "album"],
		showMediaInfo: true,
		showBlurredBackdrop: true,
		transitionSpeed: "500ms"
	});
	const slot1 = stage.children[0];
	const img1 = slot1.children[1];
	img1.onload();
	assert.equal(slot1.style.opacity, "1");
	assert.equal(infoDiv.style.display, "");

	// 2. Incoming Slide 2 errors before onload -> broken slot2 removed, healthy slot1 preserved, single MEDIABACKGROUND_LOAD_ERROR
	instance.socketNotificationReceived("MEDIABACKGROUND_DISPLAY", {
		identifier: instance.identifier,
		assetId: "vid-broken-incoming",
		type: "VIDEO",
		durationSeconds: 10,
		playbackWindowSeconds: 12,
		videoFps: 30,
		backdropUrl: "/mediabackground/backdrop/vid-broken-incoming?source=0",
		index: 2,
		total: 5,
		showMediaInfo: true,
		showBlurredBackdrop: true,
		transitionSpeed: "500ms"
	});
	assert.equal(stage.children.length, 2);
	const slot2 = stage.children[1];
	const img2 = slot2.children[1];
	const errHandler2 = img2.onerror;
	errHandler2();
	// Even if browser fires a duplicate error callback reference, it must not emit a second skip
	errHandler2();

	assert.equal(img2.src, "");
	assert.equal(img2.onerror, null);
	assert.equal(stage.children.length, 1, "healthy previous slide must remain visible when incoming slide fails");
	assert.equal(stage.children[0], slot1);
	assert.equal(infoDiv.style.display, "", "healthy slide overlay remains visible");
	assert.equal(sentNotifications.length, 1);
	assert.deepEqual(sentNotifications[0], {
		name: "MEDIABACKGROUND_LOAD_ERROR",
		payload: { identifier: instance.identifier, assetId: "vid-broken-incoming" }
	});

	// 3. Active VIDEO MJPEG stream loads frame 1 + completes crossfade, then errors mid-flight -> tears down broken <img>, hides stale overlay, notifies helper once
	sentNotifications.length = 0;
	instance.socketNotificationReceived("MEDIABACKGROUND_DISPLAY", {
		identifier: instance.identifier,
		assetId: "vid-midstream-drop",
		type: "VIDEO",
		durationSeconds: 20,
		playbackWindowSeconds: 12,
		videoFps: 30,
		backdropUrl: "/mediabackground/backdrop/vid-midstream-drop?source=0",
		index: 3,
		total: 5,
		exifInfo: { dateTimeOriginal: "2024-06-01T10:00:00Z" },
		infoFields: ["date"],
		showMediaInfo: true,
		showBlurredBackdrop: true,
		transitionSpeed: "500ms"
	});
	const slot3 = stage.children[1];
	const img3 = slot3.children[1];
	img3.onload();
	slot3.dispatchEvent({ type: "transitionend" });
	assert.equal(stage.children.length, 1);
	assert.equal(stage.children[0], slot3);

	const midStreamErrHandler = img3.onerror;
	midStreamErrHandler();
	midStreamErrHandler();

	assert.equal(img3.src, "");
	assert.equal(stage.children.length, 0, "broken mid-stream <img> slot must be torn down cleanly");
	assert.equal(infoDiv.style.display, "none", "overlay hidden when no active slide remains on stage");
	assert.equal(sentNotifications.length, 1);
	assert.deepEqual(sentNotifications[0], {
		name: "MEDIABACKGROUND_LOAD_ERROR",
		payload: { identifier: instance.identifier, assetId: "vid-midstream-drop" }
	});
});
