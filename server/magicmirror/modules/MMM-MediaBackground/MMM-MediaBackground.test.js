"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const fs = require("node:fs");
const path = require("node:path");
const http = require("node:http");

const { registerMediaRoutes } = require("./media_pipe");
const { createSlideshowController } = require("./slideshow_controller");
const MediaBackgroundModule = require("./MMM-MediaBackground");

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
