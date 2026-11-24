"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const http = require("node:http");

const {
	normalizeConfig,
	fetchSourceAssets,
	registerMediaRoutes,
	createSlideshowController
} = require("./slideshow_backend");
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
		use() {},
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

		setAttribute(name, value) {
			this.attributes.set(name, String(value));
			if (name === "src") {
				this._src = String(value);
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

		removeEventListener(type, fn) {
			const list = this._listeners.get(type);
			if (!list) return;
			const idx = list.indexOf(fn);
			if (idx >= 0) list.splice(idx, 1);
		}

		dispatchEvent(event) {
			const list = this._listeners.get(event.type) || [];
			for (const fn of [...list]) {
				fn(event);
			}
		}

		querySelectorAll(predicate) {
			const out = [];
			const walk = (node) => {
				for (const child of node.children) {
					if (predicate(child)) out.push(child);
					walk(child);
				}
			};
			walk(this);
			return out;
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

test("normalizeConfig supports first-party keys and normalizes legacy immichConfigs, showImageInfo, imageInfo, and showBlurredImageForBlackBars aliases", () => {
	const firstParty = normalizeConfig({
		slideshowSpeed: 15000,
		transitionSpeed: "1.5s",
		backgroundSize: "contain",
		showBlurredBackdrop: true,
		showMediaInfo: true,
		infoFields: ["date", "geo", "count", "album"],
		dateFormat: "DD-MM-YYYY dddd HH:mm",
		sources: [
			{
				url: "http://photo-api:2283/",
				apiKey: "secret-1",
				mode: "anniversary",
				anniversaryDatesBack: 45,
				anniversaryDatesForward: 45,
				anniversaryStartYear: 2012,
				anniversaryEndYear: 2025,
				querySize: 80
			}
		]
	});

	assert.equal(firstParty.slideshowSpeed, 15000);
	assert.equal(firstParty.transitionSpeed, "1.5s");
	assert.equal(firstParty.backgroundSize, "contain");
	assert.equal(firstParty.showBlurredBackdrop, true);
	assert.equal(firstParty.showMediaInfo, true);
	assert.deepEqual(firstParty.infoFields, ["date", "geo", "count", "album"]);
	assert.equal(firstParty.sources.length, 1);
	assert.equal(firstParty.sources[0].url, "http://photo-api:2283");
	assert.equal(firstParty.sources[0].apiKey, "secret-1");
	assert.equal(firstParty.sources[0].mode, "anniversary");
	assert.equal(firstParty.sources[0].querySize, 80);

	const legacy = normalizeConfig({
		slideshowSpeed: 30000,
		backgroundSize: "contain",
		showImageInfo: false,
		showBlurredImageForBlackBars: false,
		immichConfigs: [
			{
				apiKey: "legacy-key",
				url: "http://photo-api:2283",
				mode: "anniversary",
				anniversaryDatesBack: 30,
				anniversaryDatesForward: 30,
				anniversaryStartYear: 2010,
				anniversaryEndYear: 2026,
				querySize: 100,
				sortImagesBy: "random",
				imageInfo: "date, geo, count, album",
				dateFormat: "DD/MM/YYYY HH:mm",
				slideshowSpeed: 12000
			},
			{
				mode: "random",
				query: { size: 500, type: "IMAGE" }
			}
		]
	});

	assert.equal(legacy.showMediaInfo, false);
	assert.equal(legacy.showBlurredBackdrop, false);
	assert.equal(legacy.sources.length, 2);
	assert.equal(legacy.sources[0].apiKey, "legacy-key");
	assert.equal(legacy.sources[0].slideshowSpeed, 12000);
	assert.deepEqual(legacy.sources[0].infoFields, ["date", "geo", "count", "album"]);
	assert.equal(legacy.sources[0].dateFormat, "DD/MM/YYYY HH:mm");
	// Second legacy entry inherits apiKey/url from first entry just like MMM-ImmichSlideShow
	assert.equal(legacy.sources[1].apiKey, "legacy-key");
	assert.equal(legacy.sources[1].url, "http://photo-api:2283");
	assert.equal(legacy.sources[1].mode, "random");
	assert.equal(legacy.sources[1].querySize, 500);
	assert.equal(legacy.sources[1].mediaType, "IMAGE");
});

test("fetchSourceAssets queries photo-api accurately for anniversary (including year-wrap), random, metadata, and memory modes", async () => {
	const received = [];
	const upstream = await startTestHttpServer((req, res) => {
		let body = "";
		req.on("data", (chunk) => {
			body += chunk;
		});
		req.on("end", () => {
			const parsedBody = body ? JSON.parse(body) : null;
			received.push({
				method: req.method,
				url: req.url,
				apiKey: req.headers["x-api-key"],
				body: parsedBody
			});

			res.setHeader("Content-Type", "application/json");
			if (req.url === "/api/search/random") {
				const tag = parsedBody.takenAfter ? parsedBody.takenAfter.slice(0, 4) : "rand";
				res.end(JSON.stringify([{ id: `asset-${tag}`, type: "IMAGE", originalPath: `${tag}/pic.jpg`, exifInfo: {} }]));
			} else if (req.url === "/api/search/metadata") {
				res.end(JSON.stringify({
					assets: {
						total: 1,
						count: 1,
						items: [{ id: "asset-meta", type: "IMAGE", originalPath: "2024/meta.jpg", exifInfo: {} }]
					}
				}));
			} else if (req.url.startsWith("/api/memories")) {
				res.end(JSON.stringify([
					{ id: "mem-1", assets: [{ id: "asset-mem", type: "IMAGE", originalPath: "2021/mem.jpg", exifInfo: {} }] }
				]));
			} else {
				res.statusCode = 404;
				res.end("{}");
			}
		});
	});

	try {
		const norm = normalizeConfig({
			sources: [
				{
					url: upstream.baseUrl,
					apiKey: "test-secret",
					mode: "anniversary",
					anniversaryDatesBack: 15,
					anniversaryDatesForward: 20,
					anniversaryStartYear: 2022,
					anniversaryEndYear: 2023,
					querySize: 50,
					sortBy: "none"
				},
				{
					url: upstream.baseUrl,
					apiKey: "test-secret",
					mode: "random",
					querySize: 25,
					mediaType: "IMAGE"
				},
				{
					url: upstream.baseUrl,
					apiKey: "test-secret",
					mode: "metadata",
					querySize: 10,
					query: { takenAfter: "2024-01-01T00:00:00Z" }
				},
				{
					url: upstream.baseUrl,
					apiKey: "test-secret",
					mode: "memory",
					numDaysToInclude: 2
				}
			]
		});

		// Dec 20 + 20 days wraps into Jan 9 of next year
		const dec20 = new Date(Date.UTC(2026, 11, 20, 12, 0, 0));
		const annivAssets = await fetchSourceAssets(norm.sources[0], { now: dec20 });
		assert.deepEqual(annivAssets.map((a) => a.id), ["asset-2022", "asset-2023"]);
		assert.equal(received[0].apiKey, "test-secret");
		assert.equal(received[0].url, "/api/search/random");
		assert.equal(received[0].body.takenAfter, "2022-12-05T00:00:00.000Z");
		assert.equal(received[0].body.takenBefore, "2023-01-09T23:59:59.999Z");
		assert.equal(received[0].body.size, 50);
		assert.equal(received[0].body.type, undefined);

		const randAssets = await fetchSourceAssets(norm.sources[1]);
		assert.deepEqual(randAssets.map((a) => a.id), ["asset-rand"]);
		assert.equal(received[2].body.type, "IMAGE");
		assert.equal(received[2].body.size, 25);

		const metaAssets = await fetchSourceAssets(norm.sources[2]);
		assert.deepEqual(metaAssets.map((a) => a.id), ["asset-meta"]);
		assert.equal(received[3].url, "/api/search/metadata");
		assert.equal(received[3].body.takenAfter, "2024-01-01T00:00:00Z");

		const memAssets = await fetchSourceAssets(norm.sources[3], { now: dec20 });
		assert.equal(memAssets.length, 2);
		assert.ok(received[4].url.startsWith("/api/memories?"));
	} finally {
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
		// 1. Preview route pipes exact JPEG bytes and headers with server-injected x-api-key
		const previewResp = await fetch(`${proxy.baseUrl}/mediabackground/preview/abc-123`);
		assert.equal(previewResp.status, 200);
		assert.equal(previewResp.headers.get("content-type"), "image/jpeg");
		assert.equal(previewResp.headers.get("etag"), "\"abc-123-1\"");
		const previewBuf = Buffer.from(await previewResp.arrayBuffer());
		assert.deepEqual(previewBuf, Buffer.from([0xff, 0xd8, 0x01, 0x02, 0xff, 0xd9]));
		assert.equal(lastAuthHeader, "injected-server-secret");

		// Rejects unsafe path traversal IDs with 400
		const badIdResp = await fetch(`${proxy.baseUrl}/mediabackground/preview/..%2Fadmin`);
		assert.equal(badIdResp.status, 400);

		// 2. Backdrop route flushes initial chunk and aborts upstream immediately when downstream disconnects
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

test("createSlideshowController falls back to next configured source when primary source pool is empty", async () => {
	const upstream = await startTestHttpServer((req, res) => {
		let body = "";
		req.on("data", (c) => { body += c; });
		req.on("end", () => {
			const parsed = body ? JSON.parse(body) : {};
			res.setHeader("Content-Type", "application/json");
			// Anniversary query has takenAfter -> return empty; random fallback has no takenAfter -> return 2 photos
			if (parsed.takenAfter) {
				res.end(JSON.stringify([]));
			} else {
				res.end(JSON.stringify([
					{
						id: "fallback-1",
						type: "IMAGE",
						originalPath: "2024/Hawaii/beach.jpg",
						albumName: "Hawaii",
						exifInfo: {
							dateTimeOriginal: "2024-08-10T14:30:00Z",
							city: "Honolulu",
							state: "Hawaii",
							country: "United States"
						}
					}
				]));
			}
		});
	});

	const sent = [];
	const controller = createSlideshowController({
		sendNotification: (name, payload) => sent.push({ name, payload })
	});

	try {
		await controller.configure("mod_1", {
			slideshowSpeed: 60000,
			sources: [
				{
					url: upstream.baseUrl,
					apiKey: "k",
					mode: "anniversary",
					anniversaryStartYear: 2023,
					anniversaryEndYear: 2023
				},
				{
					url: upstream.baseUrl,
					apiKey: "k",
					mode: "random",
					querySize: 10
				}
			]
		});

		assert.equal(sent.length, 1);
		assert.equal(sent[0].name, "MEDIABACKGROUND_DISPLAY");
		assert.equal(sent[0].payload.assetId, "fallback-1");
		assert.equal(sent[0].payload.previewUrl, "/mediabackground/preview/fallback-1?source=1");
		assert.equal(sent[0].payload.backdropUrl, "/mediabackground/backdrop/fallback-1?source=1");
		assert.equal(sent[0].payload.albumName, "Hawaii");
		assert.equal(sent[0].payload.index, 1);
		assert.equal(sent[0].payload.total, 1);

		// Restores redacted **SECRET_0** apiKey from serverModuleConfig and skips broken image on error
		const secretSent = [];
		const secretUpstreamKeys = [];
		const secretServer = await startTestHttpServer((req, res) => {
			secretUpstreamKeys.push(req.headers["x-api-key"]);
			res.setHeader("Content-Type", "application/json");
			res.end(JSON.stringify([
				{ id: "broken-1", type: "IMAGE", originalPath: "a.jpg", exifInfo: {} },
				{ id: "healthy-2", type: "IMAGE", originalPath: "b.jpg", exifInfo: {} }
			]));
		});
		const ctrl2 = createSlideshowController({
			sendNotification: (name, payload) => secretSent.push({ name, payload })
		});
		try {
			await ctrl2.configure(
				"mod_secret",
				{ sources: [{ url: secretServer.baseUrl, apiKey: "**SECRET_0**", mode: "random" }] },
				{ sources: [{ url: secretServer.baseUrl, apiKey: "real-unredacted-key", mode: "random" }] }
			);
			assert.equal(secretUpstreamKeys[0], "real-unredacted-key");
			assert.equal(secretSent[0].payload.assetId, "broken-1");

			ctrl2.skipBrokenAsset("mod_secret", "broken-1");
			assert.equal(secretSent[1].payload.assetId, "healthy-2");
		} finally {
			ctrl2.stopAll();
			await secretServer.close();
		}
	} finally {
		controller.stopAll();
		await upstream.close();
	}
});

test("MMM-MediaBackground browser DOM maintains <= 2 crossfade slots, uses zero CSS backdrop-filter, clears img.src on teardown/suspend, and formats date/geo/count/album overlays", () => {
	const fakeDoc = createFakeDomEnvironment();
	const sentSocket = [];

	const instance = Object.create(MediaBackgroundModule);
	instance.name = "MMM-MediaBackground";
	instance.identifier = "module_1_MMM-MediaBackground";
	instance.documentRef = fakeDoc;
	instance.config = Object.assign({}, MediaBackgroundModule.defaults, {
		slideshowSpeed: 12000,
		transitionSpeed: "500ms",
		backgroundSize: "contain",
		showBlurredBackdrop: true,
		showMediaInfo: true,
		infoFields: ["date", "geo", "count", "album"],
		dateFormat: "DD-MM-YYYY HH:mm",
		sources: [{ url: "http://photo-api:2283", apiKey: "redacted" }]
	});
	instance.sendSocketNotification = (name, payload) => sentSocket.push({ name, payload });

	instance.start();
	const root = instance.getDom();

	const stage = root.children[0];
	const infoDiv = root.children[1];
	assert.equal(stage.className, "mmmb-stage");
	assert.ok(infoDiv.className.includes("info"));
	assert.ok(infoDiv.className.includes("bottomRight"));

	// Dispatch Slide 1
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
		}
	});

	assert.equal(stage.children.length, 1);
	const slot1 = stage.children[0];
	const backdrop1 = slot1.children[0];
	const img1 = slot1.children[1];
	assert.equal(backdrop1.className, "mmmb-backdrop");
	assert.equal(backdrop1.style.backgroundImage, "url(\"/mediabackground/backdrop/photo-1?source=0\")");
	assert.equal(backdrop1.style.backgroundSize, "cover");
	assert.equal(backdrop1.style.backdropFilter, undefined);
	assert.equal(slot1.style.backdropFilter, undefined);
	assert.equal(img1.tagName, "IMG");
	assert.equal(img1.style.objectFit, "contain");
	assert.equal(img1.src, "/mediabackground/preview/photo-1?source=0");

	// Simulate Slide 1 onload -> becomes visible and populates overlay
	img1.onload();
	assert.equal(slot1.style.opacity, "1");
	assert.equal(infoDiv.children[0].tagName, "HEADER");
	assert.equal(infoDiv.children[0].className, "infoDivHeader");
	assert.ok(infoDiv.textContent.includes("3 of 42"));
	assert.ok(infoDiv.textContent.includes("Saigon Trip"));
	assert.ok(infoDiv.textContent.includes("15-07-2023 10:30"));
	assert.ok(infoDiv.textContent.includes("Ho Chi Minh City, Vietnam"));

	// Dispatch Slide 2 -> crossfade has 2 slots during transition
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
		}
	});

	assert.equal(stage.children.length, 2, "at most 2 slots during crossfade");
	const slot2 = stage.children[1];
	const img2 = slot2.children[1];
	img2.onload();

	// Dispatch Slide 3 BEFORE Slide 2's transitionend fires -> must prune oldest slot immediately so stage NEVER exceeds 2 slots
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
		}
	});

	assert.equal(stage.children.length, 2, "strict 2-slot cap even under rapid consecutive slides");
	assert.equal(img1.src, "", "evicted outgoing slot1 must clear img.src immediately");

	const slot3 = stage.children[1];
	const img3 = slot3.children[1];
	img3.onload();
	slot3.dispatchEvent({ type: "transitionend" });

	assert.equal(stage.children.length, 1, "after transitionend, only 1 active slot remains");
	assert.equal(img2.src, "", "outgoing slot2 img.src cleared on transition completion");
	assert.equal(img3.src, "/mediabackground/preview/photo-3?source=0");
	assert.ok(infoDiv.textContent.includes("5 of 42"));
	assert.ok(infoDiv.textContent.includes("Family Reunion"));
	assert.ok(infoDiv.textContent.includes("25-12-2021 08:05"));
	assert.ok(!infoDiv.textContent.includes("Los Angeles"));

	// Suspend clears active img.src immediately
	instance.suspend();
	assert.equal(img3.src, "", "suspend() must clear active img.src");
	assert.equal(stage.children.length, 0);
});
