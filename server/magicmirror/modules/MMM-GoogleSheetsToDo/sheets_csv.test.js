"use strict";

const test = require("node:test");
const assert = require("node:assert/strict");
const {
	normalizeSheetCsvUrl,
	parseCsvRows,
	parseCheckboxCell,
	extractInlineCheckbox,
	parseTodoCsv
} = require("./sheets_csv");

test("normalizeSheetCsvUrl converts Google Sheet edit URLs, raw IDs, published URLs, and sheet names", () => {
	assert.equal(
		normalizeSheetCsvUrl({
			spreadsheetUrl: "https://docs.google.com/spreadsheets/d/1AbCdEfGhIjKlMnOpQrStUvWxYz123456/edit?usp=sharing#gid=42"
		}),
		"https://docs.google.com/spreadsheets/d/1AbCdEfGhIjKlMnOpQrStUvWxYz123456/export?format=csv&gid=42"
	);

	assert.equal(
		normalizeSheetCsvUrl({
			spreadsheetUrl: "1AbCdEfGhIjKlMnOpQrStUvWxYz123456",
			gid: "0"
		}),
		"https://docs.google.com/spreadsheets/d/1AbCdEfGhIjKlMnOpQrStUvWxYz123456/export?format=csv&gid=0"
	);

	assert.equal(
		normalizeSheetCsvUrl({
			spreadsheetUrl: "https://docs.google.com/spreadsheets/d/1AbCdEfGhIjKlMnOpQrStUvWxYz123456/edit",
			sheet: "Việc cần làm"
		}),
		"https://docs.google.com/spreadsheets/d/1AbCdEfGhIjKlMnOpQrStUvWxYz123456/gviz/tq?tqx=out:csv&sheet=Vi%E1%BB%87c%20c%E1%BA%A7n%20l%C3%A0m"
	);

	assert.equal(
		normalizeSheetCsvUrl({
			spreadsheetUrl: "https://docs.google.com/spreadsheets/d/e/2PACX-1vPubIdExample12345/pubhtml?gid=99&single=true"
		}),
		"https://docs.google.com/spreadsheets/d/e/2PACX-1vPubIdExample12345/pub?gid=99&single=true&output=csv"
	);

	assert.equal(
		normalizeSheetCsvUrl({
			spreadsheetUrl: "http://localhost:9999/todo.csv"
		}),
		"http://localhost:9999/todo.csv"
	);

	assert.throws(
		() => normalizeSheetCsvUrl({ spreadsheetUrl: "https://docs.google.com/spreadsheets/d/YOUR_SPREADSHEET_ID/edit" }),
		/spreadsheetUrl is not configured/
	);
});

test("parseCsvRows handles UTF-8 BOM, RFC 4180 quotes, commas, and Vietnamese text", () => {
	const csv = "\uFEFF\"Done\",\"Việc cần làm\"\r\nFALSE,\"Mua sữa, loại ít đường\"\r\nFALSE,\"Mua \"\"trứng\"\" gà\"\r\n";
	assert.deepEqual(parseCsvRows(csv), [
		["Done", "Việc cần làm"],
		["FALSE", "Mua sữa, loại ít đường"],
		["FALSE", "Mua \"trứng\" gà"]
	]);
});

test("parseTodoCsv parses single-column tasks and strips inline checkboxes", () => {
	const csv = [
		"Việc cần làm",
		"▢ Mua sữa",
		"Mua trứng",
		"Nộp tiền bh",
		"☑ Đã xong"
	].join("\n");

	assert.deepEqual(parseTodoCsv(csv), [
		{ title: "Mua sữa", completed: false, dueDate: null },
		{ title: "Mua trứng", completed: false, dueDate: null },
		{ title: "Nộp tiền bh", completed: false, dueDate: null }
	]);
});

test("parseTodoCsv parses checkbox-first and checkbox-second multi-column sheets", () => {
	const checkboxFirst = [
		"FALSE,Mua sữa",
		"TRUE,Đã mua bánh",
		"FALSE,Mua trứng",
		"FALSE,Nộp tiền bh"
	].join("\n");

	assert.deepEqual(parseTodoCsv(checkboxFirst), [
		{ title: "Mua sữa", completed: false, dueDate: null },
		{ title: "Mua trứng", completed: false, dueDate: null },
		{ title: "Nộp tiền bh", completed: false, dueDate: null }
	]);

	const checkboxSecond = [
		"Task,Status,Due",
		"Mua sữa,FALSE,2026-05-01",
		"Mua trứng,x,2026-05-02",
		"Nộp tiền bh,,2026-05-03"
	].join("\n");

	assert.deepEqual(parseTodoCsv(checkboxSecond), [
		{ title: "Mua sữa", completed: false, dueDate: "2026-05-01" },
		{ title: "Nộp tiền bh", completed: false, dueDate: "2026-05-03" }
	]);

	assert.equal(parseTodoCsv(checkboxSecond, { showCompleted: true }).length, 3);
});

test("parseCheckboxCell and extractInlineCheckbox classify checkbox tokens", () => {
	assert.equal(parseCheckboxCell("TRUE"), true);
	assert.equal(parseCheckboxCell("x"), true);
	assert.equal(parseCheckboxCell("FALSE"), false);
	assert.equal(parseCheckboxCell("", true), false);
	assert.equal(parseCheckboxCell("Mua sữa"), null);
	assert.deepEqual(extractInlineCheckbox("▢ Mua sữa"), { title: "Mua sữa", completed: false });
	assert.deepEqual(extractInlineCheckbox("[x] Mua trứng"), { title: "Mua trứng", completed: true });
});

test("parseTodoCsv rejects HTML login pages returned for private sheets", () => {
	assert.throws(
		() => parseTodoCsv("<!DOCTYPE html><html><body>Sign in to Google Accounts</body></html>"),
		/Received HTML instead of CSV/
	);
});
