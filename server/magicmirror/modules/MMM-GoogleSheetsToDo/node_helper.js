"use strict";

const NodeHelper = require("node_helper");
const Log = require("logger");
const { normalizeSheetCsvUrl, parseTodoCsv } = require("./sheets_csv");

const FETCH_TIMEOUT_MS = 15000;

module.exports = NodeHelper.create({
	start() {
		this.inFlight = new Map();
	},

	socketNotificationReceived(notification, payload) {
		if (notification === "FETCH_GOOGLE_SHEETS_TODO" && payload && payload.id) {
			this.fetchTodos(payload);
		}
	},

	async fetchTodos(config) {
		if (this.inFlight.get(config.id)) {
			return;
		}
		this.inFlight.set(config.id, true);

		const controller = new AbortController();
		const timeout = setTimeout(() => controller.abort(), FETCH_TIMEOUT_MS);

		try {
			const url = normalizeSheetCsvUrl({
				spreadsheetUrl: config.spreadsheetUrl,
				gid: config.gid,
				sheet: config.sheet
			});

			const response = await fetch(url, {
				method: "GET",
				redirect: "follow",
				cache: "no-store",
				headers: {
					"Cache-Control": "no-cache",
					Pragma: "no-cache",
					Accept: "text/csv,text/plain;q=0.9,*/*;q=0.8"
				},
				signal: controller.signal
			});

			if (!response.ok) {
				throw new Error(`Google Sheets HTTP ${response.status} ${response.statusText}`);
			}

			const csvText = await response.text();
			const items = parseTodoCsv(csvText, {
				showCompleted: Boolean(config.showCompleted),
				itemLimit: Number(config.itemLimit) > 0 ? Number(config.itemLimit) : 200
			});

			this.sendSocketNotification(`GOOGLE_SHEETS_TODO_DATA_${config.id}`, {
				id: config.id,
				items,
				updatedAt: new Date().toISOString()
			});
		} catch (err) {
			const message = err && err.name === "AbortError" ? "Google Sheets request timed out" : (err && err.message ? err.message : String(err));
			Log.error(`[MMM-GoogleSheetsToDo] ${message}`);
			this.sendSocketNotification(`GOOGLE_SHEETS_TODO_ERROR_${config.id}`, {
				id: config.id,
				error: message
			});
		} finally {
			clearTimeout(timeout);
			this.inFlight.delete(config.id);
		}
	}
});
