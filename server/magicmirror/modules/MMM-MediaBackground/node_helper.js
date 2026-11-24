"use strict";

const NodeHelper = require("node_helper");
const Log = require("logger");
const { registerMediaRoutes } = require("./media_pipe");
const { createSlideshowController } = require("./slideshow_controller");

module.exports = NodeHelper.create({
	requiresVersion: "2.1.0",

	start() {
		this.routesRegistered = false;
		this.controller = createSlideshowController({
			sendNotification: (notification, payload) => {
				this.sendSocketNotification(notification, payload);
			},
			logger: Log
		});
		this.ensureRoutes();
	},

	ensureRoutes() {
		if (this.routesRegistered || !this.expressApp || !this.controller) {
			return;
		}
		registerMediaRoutes(
			this.expressApp,
			(req) => this.controller.resolveSourceForRequest(req),
			Log
		);
		this.routesRegistered = true;
	},

	stop() {
		if (this.controller) {
			this.controller.stopAll();
		}
	},

	socketNotificationReceived(notification, payload) {
		if (!payload || !payload.identifier || !this.controller) {
			return;
		}
		this.ensureRoutes();

		const serverConfig = typeof this.getServerModuleConfig === "function"
			? this.getServerModuleConfig()
			: null;

		switch (notification) {
			case "MEDIABACKGROUND_INIT":
				this.controller.configure(payload.identifier, payload.config || {}, serverConfig).catch((err) => {
					Log.error(`[MMM-MediaBackground] Failed to configure slideshow: ${err.message}`);
				});
				break;
			case "MEDIABACKGROUND_SUSPEND":
				this.controller.suspend(payload.identifier);
				break;
			case "MEDIABACKGROUND_RESUME":
				this.controller.resume(payload.identifier);
				break;
			case "MEDIABACKGROUND_LOAD_ERROR":
				this.controller.skipBrokenAsset(payload.identifier, payload.assetId);
				break;
			default:
				break;
		}
	}
});
