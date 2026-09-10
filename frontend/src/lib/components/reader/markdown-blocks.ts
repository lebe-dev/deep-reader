// Markdown block parser for the reader — turns an article's raw Markdown
// (original_text) plus its deterministic word tokens into a structured block
// tree the reader renders, WITHOUT losing token interactivity.
//
// The backend only marks an article content_format = "markdown" when its
// original_text is genuine Markdown (see internal/markdown.DetectFormat). In
// that case original_text still contains the Markdown markers (`#`, `**`, `>`,
// `-`, `|`, …) at their original byte offsets, so the word tokens — whose
// offsets index into original_text — remain valid: markers are punctuation the
// tokenizer skips, living in the gaps between word tokens.
//
// This module reconstructs the document structure (headings, paragraphs, lists,
// blockquotes, fenced code, tables, thematic breaks) and inline emphasis
// (bold / italic / inline code / strikethrough) from those markers, emitting:
//   - block elements the reader wraps in <h1>/<p>/<ul>/<blockquote>/…, and
//   - inline word segments that keep their global token `index` (so tap-to-
//     translate, progress tracking, and sentence long-press all keep working),
//     each tagged with the emphasis marks active at its position.
//
// Markers themselves are dropped from the visible output. It is a pragmatic,
// line-based parser (not a full CommonMark implementation): the failure mode of
// an unhandled construct is a cosmetic stray marker, matching the backend's
// own text.go philosophy.

import type { Token } from '$lib/types';
import { buildByteToStrMap, findMarkdownSpans, type MarkdownSpan } from './reader-utils';

// ---------------------------------------------------------------------------
// Public types
// ---------------------------------------------------------------------------

/** Inline emphasis applied to a run of text. Rendered as CSS, not nested tags,
 *  so word segments stay individually interactive. */
export type InlineMark = 'strong' | 'em' | 'code' | 'strike';

/** A single inline render unit inside a block. Word segments carry the global
 *  token index and stay interactive; gaps/images/links mirror the plain reader. */
export type InlineSegment =
	| { kind: 'word'; text: string; index: number; marks: InlineMark[] }
	| { kind: 'gap'; text: string; marks: InlineMark[] }
	| { kind: 'image'; alt: string; url: string }
	| { kind: 'link'; text: string; url: string; marks: InlineMark[] };

/** A structural block parsed from the Markdown. */
export type BlockBody =
	| { kind: 'heading'; level: number; inline: InlineSegment[] }
	| { kind: 'paragraph'; inline: InlineSegment[] }
	| { kind: 'list'; ordered: boolean; items: InlineSegment[][] }
	| { kind: 'code'; text: string }
	| { kind: 'table'; header: InlineSegment[][]; rows: InlineSegment[][][] }
	| { kind: 'hr' };

/**
 * A block plus its blockquote nesting.
 *
 * `depth` is how many `>` markers the block sits behind — 0 for ordinary body
 * text, and for a comment thread (`internal/comments`) the reply depth of the
 * comment it belongs to. `breakDepth` is the shallowest blank line seen since
 * the previous block: a blank line ends every quote level deeper than itself,
 * which is what separates two quotes at the same depth from one quote that
 * simply continues. Both are consumed by `nestBlocks`.
 */
export type Block = BlockBody & { depth: number; breakDepth: number };

/** A quote level in the nested tree: the container `nestBlocks` wraps a run of
 *  deeper blocks in. For a comment thread this is one reply level — the reader
 *  draws it as an indented column with a rail. */
export interface QuoteNode {
	kind: 'quote';
	depth: number;
	children: BlockNode[];
}

/** A node of the nested block tree: either a block or a quote level. */
export type BlockNode = Block | QuoteNode;

// ---------------------------------------------------------------------------
// Block-level patterns (mirroring internal/markdown/text.go)
// ---------------------------------------------------------------------------

const headingRe = /^(\s{0,3})(#{1,6})(\s+)(.*)$/;
const blockquoteRe = /^\s{0,3}>\s?/;
const listItemRe = /^(\s*)([-*+]|\d+[.)])(\s+)(.*)$/;
const tableDelimRe = /^\|?[\s:|-]+\|[\s:|-]*$/;

/** Returns "```" or "~~~" if trimmed opens/closes a code fence, else "". */
function fenceMarker(trimmed: string): string {
	if (trimmed.startsWith('```')) return '```';
	if (trimmed.startsWith('~~~')) return '~~~';
	return '';
}

/** Three or more of the same marker (-, *, _), spaces allowed, nothing else. */
function isHorizontalRule(trimmed: string): boolean {
	let marker = '';
	let count = 0;
	for (const ch of trimmed) {
		if (ch === ' ' || ch === '\t') continue;
		if (ch === '-' || ch === '*' || ch === '_') {
			if (count === 0) marker = ch;
			else if (ch !== marker) return false;
			count++;
		} else {
			return false;
		}
	}
	return count >= 3;
}

/** A Markdown table delimiter row, e.g. `| --- | :--: |`. */
function isTableDelimiter(trimmed: string): boolean {
	return trimmed.includes('|') && trimmed.includes('-') && tableDelimRe.test(trimmed);
}

// ---------------------------------------------------------------------------
// Line model — text plus its [start, end) string offsets (end excludes "\n").
// ---------------------------------------------------------------------------

interface Line {
	text: string;
	start: number;
	end: number;
}

/**
 * Strip the blockquote markers from a line, returning the remaining content
 * (with its offsets shifted past the markers) and how many markers there were.
 * Nesting depth is read here once, so every construct below — headings, lists,
 * code fences — parses the same inside a quote as outside it.
 */
function stripQuoteMarkers(line: Line): { line: Line; depth: number } {
	let { text, start } = line;
	let depth = 0;
	for (;;) {
		const m = blockquoteRe.exec(text);
		if (!m) break;
		depth++;
		start += m[0].length;
		text = text.slice(m[0].length);
	}
	return { line: { text, start, end: line.end }, depth };
}

function splitLines(text: string): Line[] {
	const lines: Line[] = [];
	let offset = 0;
	for (const raw of text.split('\n')) {
		lines.push({ text: raw, start: offset, end: offset + raw.length });
		offset += raw.length + 1; // +1 for the consumed "\n"
	}
	return lines;
}

// ---------------------------------------------------------------------------
// String-space tokens — token offsets are byte offsets; the reader works in JS
// string space, so map them once up front.
// ---------------------------------------------------------------------------

interface SToken {
	index: number;
	text: string;
	start: number; // string index (inclusive)
	end: number; // string index (exclusive)
}

interface Range {
	start: number;
	end: number;
}

// ---------------------------------------------------------------------------
// Inline emphasis scanning
// ---------------------------------------------------------------------------

/** Per-character emphasis state for a substring: which marks apply and which
 *  characters are markers to hide. */
interface EmphasisScan {
	marks: InlineMark[][];
	hide: boolean[];
}

const MARK_ORDER: InlineMark[] = ['strong', 'em', 'code', 'strike'];

function isSpace(ch: string | undefined): boolean {
	return ch === undefined || ch === ' ' || ch === '\t' || ch === '\n';
}

function isWordChar(ch: string | undefined): boolean {
	return ch !== undefined && /[\p{L}\p{N}]/u.test(ch);
}

/**
 * Scan a clean substring (no image/link syntax) for emphasis delimiters,
 * producing, per character, the active marks and whether the character is a
 * delimiter to hide. Uses a toggle model with "flanking-lite" rules: an opening
 * delimiter must be followed by a non-space, a closing one preceded by a
 * non-space — so stray `*`/`_` in prose (e.g. "5 * 3") stay literal. Inline
 * code (`` ` ``) suppresses all other emphasis until it closes.
 */
function scanEmphasis(s: string): EmphasisScan {
	const marks: InlineMark[][] = new Array(s.length);
	const hide: boolean[] = new Array(s.length).fill(false);
	const active = { strong: false, em: false, code: false, strike: false };

	const current = (): InlineMark[] => MARK_ORDER.filter((m) => active[m]);

	// canToggle: flanking-lite. markerLen is the delimiter length.
	const canToggle = (on: boolean, i: number, markerLen: number): boolean => {
		if (!on) return !isSpace(s[i + markerLen]); // opening → next is content
		return !isSpace(s[i - 1]); // closing → previous is content
	};

	let i = 0;
	while (i < s.length) {
		const c = s[i];
		const n = s[i + 1];

		if (c === '`') {
			active.code = !active.code;
			hide[i] = true;
			marks[i] = [];
			i++;
			continue;
		}
		if (active.code) {
			// Raw code content keeps the code mark; nothing else toggles inside.
			marks[i] = current();
			i++;
			continue;
		}

		if ((c === '*' && n === '*') || (c === '_' && n === '_')) {
			if (canToggle(active.strong, i, 2)) {
				active.strong = !active.strong;
				hide[i] = hide[i + 1] = true;
				marks[i] = marks[i + 1] = current();
				i += 2;
				continue;
			}
		} else if (c === '~' && n === '~') {
			if (canToggle(active.strike, i, 2)) {
				active.strike = !active.strike;
				hide[i] = hide[i + 1] = true;
				marks[i] = marks[i + 1] = current();
				i += 2;
				continue;
			}
		} else if (c === '*' || c === '_') {
			// Underscore inside a word (snake_case) is never a delimiter.
			const intraword = c === '_' && isWordChar(s[i - 1]) && isWordChar(s[i + 1]);
			if (!intraword && canToggle(active.em, i, 1)) {
				active.em = !active.em;
				hide[i] = true;
				marks[i] = current();
				i++;
				continue;
			}
		}

		marks[i] = current();
		i++;
	}

	return { marks, hide };
}

// ---------------------------------------------------------------------------
// Inline segment construction
// ---------------------------------------------------------------------------

/** Build inline segments for an ordered list of content ranges. Ranges are
 *  joined with a single space so soft-wrapped lines / stripped markers don't
 *  fuse adjacent words. */
function buildInline(
	stokens: SToken[],
	text: string,
	ranges: Range[],
	spans: MarkdownSpan[]
): InlineSegment[] {
	const out: InlineSegment[] = [];
	ranges.forEach((range, ri) => {
		if (ri > 0) out.push({ kind: 'gap', text: ' ', marks: [] });
		appendRange(out, stokens, text, range, spans);
	});
	return out;
}

/** Append the inline segments for a single content range, splitting around any
 *  markdown image/link spans and applying emphasis marks to the rest. */
function appendRange(
	out: InlineSegment[],
	stokens: SToken[],
	text: string,
	range: Range,
	spans: MarkdownSpan[]
): void {
	const within = spans
		.filter((sp) => sp.start >= range.start && sp.end <= range.end)
		.sort((a, b) => a.start - b.start);

	let cursor = range.start;
	for (const sp of within) {
		if (sp.start < cursor) continue; // overlap guard
		appendEmphasized(out, stokens, text, { start: cursor, end: sp.start });
		out.push(
			sp.kind === 'image'
				? { kind: 'image', alt: sp.text, url: sp.url }
				: { kind: 'link', text: sp.text, url: sp.url, marks: [] }
		);
		cursor = sp.end;
	}
	appendEmphasized(out, stokens, text, { start: cursor, end: range.end });
}

/** Append word + gap segments for a clean range (no image/link syntax),
 *  applying emphasis marks and dropping hidden marker characters. */
function appendEmphasized(
	out: InlineSegment[],
	stokens: SToken[],
	text: string,
	range: Range
): void {
	if (range.end <= range.start) return;

	const slice = text.slice(range.start, range.end);
	const { marks, hide } = scanEmphasis(slice);

	// Reveal the visible text of [a, b) with hidden markers removed, relative to
	// the slice. marksAt picks the marks active at a slice position.
	const visible = (a: number, b: number): string => {
		let s = '';
		for (let i = a; i < b; i++) if (!hide[i]) s += slice[i];
		return s;
	};
	const marksAt = (i: number): InlineMark[] => marks[i] ?? [];

	let cursor = range.start; // absolute string index
	for (const t of stokens) {
		if (t.start >= range.end) break;
		if (t.end <= cursor) continue;
		if (t.start < cursor) continue; // token already consumed by a prior range

		if (t.start > cursor) {
			const g = visible(cursor - range.start, t.start - range.start);
			if (g) out.push({ kind: 'gap', text: g, marks: [] });
		}
		out.push({ kind: 'word', text: t.text, index: t.index, marks: marksAt(t.start - range.start) });
		cursor = t.end;
	}
	if (cursor < range.end) {
		const g = visible(cursor - range.start, range.end - range.start);
		if (g) out.push({ kind: 'gap', text: g, marks: [] });
	}
}

// ---------------------------------------------------------------------------
// Table parsing
// ---------------------------------------------------------------------------

/** Split a table row line into per-cell ranges (absolute string offsets),
 *  dropping the empty cells produced by leading/trailing pipes. */
function splitCells(line: Line): Range[] {
	const cells: Range[] = [];
	let segStart = line.start;
	for (let i = 0; i < line.text.length; i++) {
		if (line.text[i] === '|') {
			cells.push({ start: segStart, end: line.start + i });
			segStart = line.start + i + 1;
		}
	}
	cells.push({ start: segStart, end: line.end });

	// Drop edge cells that are empty (artifacts of `| a | b |` border pipes).
	const nonEmpty = (r: Range) => cellText(line, r) !== '';
	if (cells.length && !nonEmpty(cells[0])) cells.shift();
	if (cells.length && !nonEmpty(cells[cells.length - 1])) cells.pop();
	return cells;
}

/** Trimmed text of a range that lies within a single line. */
function cellText(line: Line, r: Range): string {
	return line.text.slice(r.start - line.start, r.end - line.start).trim();
}

// ---------------------------------------------------------------------------
// Top-level parse
// ---------------------------------------------------------------------------

/**
 * Parse Markdown into a block tree, preserving the interactive word tokens.
 *
 * tokens are the article's deterministic word tokens (byte offsets into the
 * UTF-8 encoding of `text`); `text` is the raw Markdown (original_text). Use
 * only when the article's content_format is "markdown"; plain articles should
 * continue to use buildRenderSegments.
 */
export function buildMarkdownBlocks(tokens: Token[], text: string): Block[] {
	const byteToStr = buildByteToStrMap(text);
	const strIdx = (byte: number): number => byteToStr[byte] ?? text.length;
	const stokens: SToken[] = tokens.map((t) => ({
		index: t.index,
		text: t.text,
		start: strIdx(t.start),
		end: strIdx(t.end)
	}));
	const spans = findMarkdownSpans(text);
	const stripped = splitLines(text).map(stripQuoteMarkers);
	const lines = stripped.map((s) => s.line);
	const depths = stripped.map((s) => s.depth);

	const inline = (ranges: Range[]): InlineSegment[] => buildInline(stokens, text, ranges, spans);

	const blocks: Block[] = [];
	let i = 0;

	// The shallowest blank line seen since the last block was pushed. A blank
	// line closes every quote level deeper than itself, so this is what tells
	// two sibling quotes apart from one quote that continues (see Block).
	let breakDepth = Number.POSITIVE_INFINITY;

	/** Push a block, stamping the nesting it was parsed at. */
	const push = (body: BlockBody, depth: number): void => {
		blocks.push({ ...body, depth, breakDepth });
		breakDepth = Number.POSITIVE_INFINITY;
	};

	const isBlockStart = (idx: number): boolean => {
		const ln = lines[idx];
		const trimmed = ln.text.trim();
		if (trimmed === '') return true;
		if (headingRe.test(ln.text) || listItemRe.test(ln.text)) return true;
		if (isHorizontalRule(trimmed) || fenceMarker(trimmed)) return true;
		if (
			ln.text.includes('|') &&
			idx + 1 < lines.length &&
			isTableDelimiter(lines[idx + 1].text.trim())
		)
			return true;
		return false;
	};

	while (i < lines.length) {
		const line = lines[i];
		const trimmed = line.text.trim();
		const depth = depths[i];

		if (trimmed === '') {
			breakDepth = Math.min(breakDepth, depth);
			i++;
			continue;
		}

		// Fenced code block — collect verbatim, drop the fences.
		const fence = fenceMarker(trimmed);
		if (fence) {
			const codeLines: string[] = [];
			i++;
			while (i < lines.length && !lines[i].text.trim().startsWith(fence)) {
				codeLines.push(lines[i].text);
				i++;
			}
			if (i < lines.length) i++; // consume closing fence
			push({ kind: 'code', text: codeLines.join('\n') }, depth);
			continue;
		}

		// Heading.
		const h = headingRe.exec(line.text);
		if (h) {
			const level = h[2].length;
			const contentStart = line.start + h[1].length + h[2].length + h[3].length;
			push(
				{ kind: 'heading', level, inline: inline([{ start: contentStart, end: line.end }]) },
				depth
			);
			i++;
			continue;
		}

		// Thematic break.
		if (isHorizontalRule(trimmed)) {
			push({ kind: 'hr' }, depth);
			i++;
			continue;
		}

		// Table: a row followed by a delimiter row.
		if (
			line.text.includes('|') &&
			i + 1 < lines.length &&
			isTableDelimiter(lines[i + 1].text.trim())
		) {
			const header = splitCells(line).map((c) => inline([c]));
			i += 2; // header + delimiter
			const rows: InlineSegment[][][] = [];
			while (
				i < lines.length &&
				depths[i] === depth &&
				lines[i].text.includes('|') &&
				lines[i].text.trim() !== ''
			) {
				rows.push(splitCells(lines[i]).map((c) => inline([c])));
				i++;
			}
			push({ kind: 'table', header, rows }, depth);
			continue;
		}

		// List — consecutive items; non-marker non-blank lines extend the last item.
		const li = listItemRe.exec(line.text);
		if (li) {
			const ordered = /\d/.test(li[2]);
			const items: InlineSegment[][] = [];
			while (i < lines.length && depths[i] === depth) {
				const cur = lines[i];
				if (cur.text.trim() === '') break;
				const m = listItemRe.exec(cur.text);
				if (m) {
					const contentStart = cur.start + m[1].length + m[2].length + m[3].length;
					items.push(inline([{ start: contentStart, end: cur.end }]));
					i++;
				} else if (items.length > 0) {
					const extra = inline([{ start: cur.start, end: cur.end }]);
					items[items.length - 1] = [
						...items[items.length - 1],
						{ kind: 'gap', text: ' ', marks: [] },
						...extra
					];
					i++;
				} else {
					break;
				}
			}
			push({ kind: 'list', ordered, items }, depth);
			continue;
		}

		// Paragraph — accumulate soft-wrapped lines up to the next block boundary.
		const ranges: Range[] = [];
		while (
			i < lines.length &&
			depths[i] === depth &&
			lines[i].text.trim() !== '' &&
			!isBlockStart(i)
		) {
			ranges.push({ start: lines[i].start, end: lines[i].end });
			i++;
		}
		if (ranges.length > 0) {
			push({ kind: 'paragraph', inline: inline(ranges) }, depth);
		} else {
			i++; // safety: never stall
		}
	}

	return blocks;
}

/**
 * Group a flat block list into the quote tree the reader renders: each level of
 * blockquote nesting becomes a QuoteNode wrapping the blocks inside it.
 *
 * A block opens as many levels as its `depth` requires and closes every level
 * deeper than the blank line that preceded it (`breakDepth`), so two comments
 * replying to the same parent share one level — the indentation rail they share
 * in the thread — while a comment that returns to a shallower level starts a
 * new one.
 */
export function nestBlocks(blocks: Block[]): BlockNode[] {
	const root: BlockNode[] = [];
	const stack: QuoteNode[] = [];

	const container = (): BlockNode[] => (stack.length ? stack[stack.length - 1].children : root);

	for (const block of blocks) {
		const keep = Math.min(block.depth, block.breakDepth);
		while (stack.length > keep) stack.pop();
		while (stack.length < block.depth) {
			const node: QuoteNode = { kind: 'quote', depth: stack.length + 1, children: [] };
			container().push(node);
			stack.push(node);
		}
		container().push(block);
	}

	return root;
}
