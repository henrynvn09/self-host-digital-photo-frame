"use strict";

const FALSE_TOKENS = new Set([
	"false",
	"0",
	"no",
	"n",
	"☐",
	"▢",
	"[ ]",
	"[]",
	"pending",
	"todo",
	"chưa"
]);

const TRUE_TOKENS = new Set([
	"true",
	"1",
	"yes",
	"y",
	"x",
	"✓",
	"✔",
	"☑",
	"▣",
	"[x]",
	"done",
	"completed",
	"xong",
	"rồi"
]);

const HEADER_KEYWORDS = new Set([
	"việc cần làm",
	"viec can lam",
	"công việc",
	"cong viec",
	"task",
	"tasks",
	"todo",
	"to-do",
	"item",
	"items",
	"title",
	"status",
	"done",
	"completed",
	"hoàn thành",
	"hoan thanh",
	"checkbox",
	"due",
	"due date",
	"hạn"
]);

const INLINE_UNCHECKED_PREFIX = /^(?:☐|▢|\[\s?\])\s+/;
const INLINE_CHECKED_PREFIX = /^(?:☑|✓|✔|▣|\[[xX]\])\s+/;
const RAW_SPREADSHEET_ID = /^[a-zA-Z0-9-_]{15,}$/;

function extractGidFromUrl(parsedUrl) {
	const queryGid = parsedUrl.searchParams.get("gid");
	if (queryGid !== null && queryGid !== "") {
		return queryGid;
	}
	const hashMatch = (parsedUrl.hash || "").match(/(?:^|[#&?])gid=([^&]+)/);
	return hashMatch ? decodeURIComponent(hashMatch[1]) : null;
}

function normalizeSheetCsvUrl({ spreadsheetUrl, gid = null, sheet = null } = {}) {
	const raw = String(spreadsheetUrl || "").trim();
	if (!raw || raw.includes("YOUR_SPREADSHEET_ID") || raw.includes("<link")) {
		throw new Error("spreadsheetUrl is not configured");
	}

	const cleanGid = gid !== null && gid !== undefined && String(gid).trim() !== "" ? String(gid).trim() : null;
	const cleanSheet = sheet !== null && sheet !== undefined && String(sheet).trim() !== "" ? String(sheet).trim() : null;

	if (RAW_SPREADSHEET_ID.test(raw)) {
		if (cleanSheet) {
			return `https://docs.google.com/spreadsheets/d/${raw}/gviz/tq?tqx=out:csv&sheet=${encodeURIComponent(cleanSheet)}`;
		}
		return `https://docs.google.com/spreadsheets/d/${raw}/export?format=csv${cleanGid ? `&gid=${encodeURIComponent(cleanGid)}` : ""}`;
	}

	let parsed;
	try {
		parsed = new URL(raw);
	} catch {
		throw new Error(`Invalid spreadsheetUrl: ${raw}`);
	}

	if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
		throw new Error(`Unsupported URL protocol in spreadsheetUrl: ${parsed.protocol}`);
	}

	if (parsed.hostname === "docs.google.com") {
		const pubMatch = parsed.pathname.match(/^\/spreadsheets\/d\/e\/([a-zA-Z0-9-_]+)/);
		if (pubMatch) {
			const pubId = pubMatch[1];
			const resolvedGid = cleanGid || extractGidFromUrl(parsed);
			const params = new URLSearchParams();
			if (resolvedGid) {
				params.set("gid", resolvedGid);
			}
			params.set("single", "true");
			params.set("output", "csv");
			return `https://docs.google.com/spreadsheets/d/e/${pubId}/pub?${params.toString()}`;
		}

		const docMatch = parsed.pathname.match(/^\/spreadsheets\/d\/([a-zA-Z0-9-_]+)/);
		if (docMatch) {
			const docId = docMatch[1];
			const resolvedSheet = cleanSheet || parsed.searchParams.get("sheet");
			if (resolvedSheet) {
				return `https://docs.google.com/spreadsheets/d/${docId}/gviz/tq?tqx=out:csv&sheet=${encodeURIComponent(resolvedSheet)}`;
			}
			const resolvedGid = cleanGid || extractGidFromUrl(parsed);
			return `https://docs.google.com/spreadsheets/d/${docId}/export?format=csv${resolvedGid ? `&gid=${encodeURIComponent(resolvedGid)}` : ""}`;
		}
	}

	return raw;
}

function parseCsvRows(csvText) {
	const input = String(csvText || "").replace(/^\uFEFF/, "");
	const rows = [];
	let row = [];
	let cell = "";
	let inQuotes = false;

	for (let i = 0; i < input.length; i += 1) {
		const ch = input[i];

		if (inQuotes) {
			if (ch === "\"") {
				if (input[i + 1] === "\"") {
					cell += "\"";
					i += 1;
				} else {
					inQuotes = false;
				}
			} else {
				cell += ch;
			}
			continue;
		}

		if (ch === "\"") {
			inQuotes = true;
		} else if (ch === ",") {
			row.push(cell.trim());
			cell = "";
		} else if (ch === "\r" || ch === "\n") {
			if (ch === "\r" && input[i + 1] === "\n") {
				i += 1;
			}
			row.push(cell.trim());
			cell = "";
			if (row.some((value) => value !== "")) {
				rows.push(row);
			}
			row = [];
		} else {
			cell += ch;
		}
	}

	row.push(cell.trim());
	if (row.some((value) => value !== "")) {
		rows.push(row);
	}

	return rows;
}

function parseCheckboxCell(cell, allowEmptyAsFalse = false) {
	const raw = String(cell ?? "").trim();
	if (raw === "") {
		return allowEmptyAsFalse ? false : null;
	}
	const normalized = raw.toLowerCase();
	if (FALSE_TOKENS.has(normalized)) {
		return false;
	}
	if (TRUE_TOKENS.has(normalized)) {
		return true;
	}
	return null;
}

function extractInlineCheckbox(rawTitle) {
	const text = String(rawTitle || "").trim();
	if (INLINE_UNCHECKED_PREFIX.test(text)) {
		return { title: text.replace(INLINE_UNCHECKED_PREFIX, "").trim(), completed: false };
	}
	if (INLINE_CHECKED_PREFIX.test(text)) {
		return { title: text.replace(INLINE_CHECKED_PREFIX, "").trim(), completed: true };
	}
	return { title: text, completed: false };
}

function isCheckboxColumn(rows, colIndex) {
	let explicitMatches = 0;
	for (const row of rows) {
		const cell = row[colIndex] ?? "";
		if (cell === "") {
			continue;
		}
		if (parseCheckboxCell(cell, false) === null) {
			return false;
		}
		explicitMatches += 1;
	}
	return explicitMatches > 0;
}

function shouldSkipHeaderRow(rows) {
	if (rows.length === 0) {
		return false;
	}
	const firstRow = rows[0];
	if (firstRow.some((cell) => HEADER_KEYWORDS.has(cell.toLowerCase()))) {
		return true;
	}
	if (rows.length > 1) {
		const dataRows = rows.slice(1);
		if (firstRow[0] && parseCheckboxCell(firstRow[0], false) === null && isCheckboxColumn(dataRows, 0)) {
			return true;
		}
		if (firstRow[1] && parseCheckboxCell(firstRow[1], false) === null && isCheckboxColumn(dataRows, 1)) {
			return true;
		}
	}
	return false;
}

function parseTodoCsv(csvText, { showCompleted = false, itemLimit = 200 } = {}) {
	const trimmed = String(csvText || "").trimStart();
	if (/^(?:<!doctype\s+html|<html\b)/i.test(trimmed)) {
		throw new Error("Received HTML instead of CSV; verify the Google Sheet is shared as 'Anyone with the link can view' or Published to the web");
	}

	const allRows = parseCsvRows(csvText);
	if (allRows.length === 0) {
		return [];
	}

	const dataRows = shouldSkipHeaderRow(allRows) ? allRows.slice(1) : allRows;
	if (dataRows.length === 0) {
		return [];
	}

	const maxCols = Math.max(...dataRows.map((row) => row.length));
	let layout = "single";
	if (maxCols >= 2) {
		if (isCheckboxColumn(dataRows, 0)) {
			layout = "checkbox-first";
		} else if (isCheckboxColumn(dataRows, 1)) {
			layout = "checkbox-second";
		} else {
			layout = "title-due";
		}
	}

	const items = [];
	for (const row of dataRows) {
		let title = "";
		let completed = false;
		let dueDate = null;

		if (layout === "single") {
			const parsed = extractInlineCheckbox(row[0] || "");
			title = parsed.title;
			completed = parsed.completed;
		} else if (layout === "checkbox-first") {
			completed = Boolean(parseCheckboxCell(row[0], true));
			title = String(row[1] || "").trim();
			dueDate = row[2] ? String(row[2]).trim() : null;
		} else if (layout === "checkbox-second") {
			title = String(row[0] || "").trim();
			completed = Boolean(parseCheckboxCell(row[1], true));
			dueDate = row[2] ? String(row[2]).trim() : null;
		} else {
			const parsed = extractInlineCheckbox(row[0] || "");
			title = parsed.title;
			completed = parsed.completed;
			dueDate = row[1] ? String(row[1]).trim() : null;
		}

		if (!title) {
			continue;
		}
		if (completed && !showCompleted) {
			continue;
		}

		items.push({ title, completed, dueDate: dueDate || null });
		if (items.length >= itemLimit) {
			break;
		}
	}

	return items;
}

function formatCalendarEventTime({ fullDayEvent, startMoment, endMoment, nowMoment }) {
	const currentYear = nowMoment.year();

	if (fullDayEvent) {
		const adjustedEnd = endMoment.clone().subtract(12, "hours");
		const effectiveEnd = adjustedEnd.isBefore(startMoment) ? startMoment.clone() : adjustedEnd;
		const sameYearAsNow = startMoment.year() === currentYear && effectiveEnd.year() === currentYear;
		const dateFmt = sameYearAsNow ? "DD/MM" : "DD/MM/YY";

		if (startMoment.isSame(effectiveEnd, "d")) {
			return startMoment.format(dateFmt);
		}
		return `${startMoment.format(dateFmt)} - ${effectiveEnd.format(dateFmt)}`;
	}

	const sameYearAsNow = startMoment.year() === currentYear && endMoment.year() === currentYear;
	const dateFmt = sameYearAsNow ? "DD/MM" : "DD/MM/YY";

	if (startMoment.isSame(endMoment, "d")) {
		if (startMoment.valueOf() === endMoment.valueOf()) {
			return `${startMoment.format(dateFmt)} ${startMoment.format("HH:mm")}`;
		}
		return `${startMoment.format(dateFmt)} ${startMoment.format("HH:mm")} - ${endMoment.format("HH:mm")}`;
	}

	return `${startMoment.format(dateFmt)} ${startMoment.format("HH:mm")} - ${endMoment.format(dateFmt)} ${endMoment.format("HH:mm")}`;
}

module.exports = {
	normalizeSheetCsvUrl,
	parseCsvRows,
	parseCheckboxCell,
	extractInlineCheckbox,
	parseTodoCsv,
	formatCalendarEventTime
};
