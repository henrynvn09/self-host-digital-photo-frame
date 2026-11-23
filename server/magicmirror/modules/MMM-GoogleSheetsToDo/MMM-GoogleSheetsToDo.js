/* global Module, Log, moment */

Module.register("MMM-GoogleSheetsToDo", {
	defaults: {
		spreadsheetUrl: "",
		gid: null,
		sheet: null,
		refreshSeconds: 60,
		showCheckbox: true,
		checkboxSymbol: "▢ ",
		checkedSymbol: "☑ ",
		showCompleted: false,
		showDueDate: false,
		dateFormat: "ddd MMM Do [ - ]",
		useRelativeDate: true,
		colorDueDate: false,
		highlightTagColor: "#E3FF30",
		hideIfEmpty: false,
		maxWidth: 450,
		itemLimit: 200,
		fade: true,
		fadePoint: 0.5
	},

	getStyles() {
		return ["MMM-GoogleSheetsToDo.css"];
	},

	getScripts() {
		return ["moment.js"];
	},

	start() {
		Log.info(`Starting module: ${this.name}`);
		this.items = [];
		this.loaded = false;
		this.error = null;

		const intervalMs = Math.max(10, Number(this.config.refreshSeconds) || 60) * 1000;
		this.fetchData();
		this.refreshTimer = setInterval(() => {
			this.fetchData();
		}, intervalMs);
	},

	suspend() {
		if (this.refreshTimer) {
			clearInterval(this.refreshTimer);
			this.refreshTimer = null;
		}
	},

	resume() {
		const intervalMs = Math.max(10, Number(this.config.refreshSeconds) || 60) * 1000;
		this.fetchData();
		if (!this.refreshTimer) {
			this.refreshTimer = setInterval(() => {
				this.fetchData();
			}, intervalMs);
		}
	},

	fetchData() {
		this.sendSocketNotification("FETCH_GOOGLE_SHEETS_TODO", {
			id: this.identifier,
			spreadsheetUrl: this.config.spreadsheetUrl,
			gid: this.config.gid,
			sheet: this.config.sheet,
			showCompleted: this.config.showCompleted,
			itemLimit: this.config.itemLimit
		});
	},

	socketNotificationReceived(notification, payload) {
		if (notification === `GOOGLE_SHEETS_TODO_DATA_${this.identifier}` && payload) {
			this.items = Array.isArray(payload.items) ? payload.items : [];
			this.loaded = true;
			this.error = null;
			if (this.config.hideIfEmpty) {
				if (this.items.length > 0) {
					this.show(1000);
				} else {
					this.hide(1000);
				}
			}
			this.updateDom();
		} else if (notification === `GOOGLE_SHEETS_TODO_ERROR_${this.identifier}` && payload) {
			this.error = payload.error || "Unable to load Google Sheets To-Do";
			if (!this.loaded) {
				this.updateDom();
			}
		}
	},

	appendFormattedTitle(container, title) {
		const tagPattern = /#[^\s#]+/g;
		let lastIndex = 0;
		let match = tagPattern.exec(title);

		while (match !== null) {
			if (match.index > lastIndex) {
				container.appendChild(document.createTextNode(title.slice(lastIndex, match.index)));
			}
			const tagSpan = document.createElement("span");
			if (this.config.highlightTagColor) {
				tagSpan.style.color = this.config.highlightTagColor;
			}
			tagSpan.textContent = match[0];
			container.appendChild(tagSpan);
			lastIndex = match.index + match[0].length;
			match = tagPattern.exec(title);
		}

		if (lastIndex < title.length) {
			container.appendChild(document.createTextNode(title.slice(lastIndex)));
		}
	},

	buildDueDateSpan(dueDate) {
		const span = document.createElement("span");
		span.classList.add("mmm-task-due-date");

		if (typeof moment !== "function") {
			span.textContent = `${dueDate} - `;
			return span;
		}

		const parsed = moment(dueDate);
		if (!parsed.isValid()) {
			span.textContent = `${dueDate} - `;
			return span;
		}

		const now = moment().startOf("day");
		const dueDay = parsed.clone().startOf("day");
		if (this.config.colorDueDate) {
			if (dueDay.isBefore(now)) {
				span.classList.add("overdue");
			} else if (dueDay.isSame(now)) {
				span.classList.add("due-today");
			} else {
				span.classList.add("upcoming");
			}
		}

		if (this.config.useRelativeDate) {
			if (dueDay.isSame(now)) {
				span.textContent = "Today - ";
				return span;
			}
			if (dueDay.isSame(now.clone().subtract(1, "day"))) {
				span.textContent = "Yesterday - ";
				return span;
			}
			if (dueDay.isSame(now.clone().add(1, "day"))) {
				span.textContent = "Tomorrow - ";
				return span;
			}
		}

		span.textContent = parsed.format(this.config.dateFormat);
		return span;
	},

	getDom() {
		const listWrapper = document.createElement("ul");
		listWrapper.style.maxWidth = `${this.config.maxWidth}px`;
		listWrapper.style.paddingLeft = "0";
		listWrapper.style.marginTop = "0";
		listWrapper.style.listStyleType = "none";
		listWrapper.classList.add("small");

		if (!this.loaded) {
			const status = document.createElement("div");
			status.className = "dimmed light xsmall";
			status.textContent = this.error ? this.error : this.translate("LOADING");
			return status;
		}

		const total = this.items.length;
		const fadePoint = Math.min(1, Math.max(0, Number(this.config.fadePoint) || 0.5));
		const startFade = total * fadePoint;
		const fadeSteps = total - startFade;

		this.items.forEach((item, index) => {
			const listItem = document.createElement("li");
			listItem.style.listStylePosition = "inside";
			listItem.style.whiteSpace = "nowrap";
			listItem.style.overflow = "hidden";
			listItem.style.textOverflow = "ellipsis";
			if (item.completed) {
				listItem.classList.add("mmm-task-completed");
			}

			if (this.config.fade && fadeSteps > 0 && index >= startFade) {
				const currentStep = index - startFade;
				listItem.style.opacity = String(1 - (1 / fadeSteps) * currentStep);
			}

			if (this.config.showCheckbox) {
				const symbol = item.completed ? this.config.checkedSymbol : this.config.checkboxSymbol;
				listItem.appendChild(document.createTextNode(symbol));
			}

			if (this.config.showDueDate && item.dueDate) {
				listItem.appendChild(this.buildDueDateSpan(item.dueDate));
			}

			const titleSpan = document.createElement("span");
			this.appendFormattedTitle(titleSpan, item.title);
			listItem.appendChild(titleSpan);

			listWrapper.appendChild(listItem);
		});

		return listWrapper;
	}
});
