import { describe, it, expect } from 'vitest';
import { buildMarkdownBlocks, type Block, type InlineSegment } from './markdown-blocks';
import type { Token } from '$lib/types';

// Minimal word tokenizer mirroring the backend contract: word tokens only
// (letters/digits/'/-), with byte offsets into the UTF-8 encoding of text.
function tokenize(text: string): Token[] {
	const tokens: Token[] = [];
	const enc = new TextEncoder();
	const re = /[\p{L}\p{N}][\p{L}\p{N}'-]*/gu;
	let index = 0;
	for (const m of text.matchAll(re)) {
		const start = enc.encode(text.slice(0, m.index)).length;
		const end = start + enc.encode(m[0]).length;
		tokens.push({ index: index++, text: m[0], start, end });
	}
	return tokens;
}

function build(text: string): Block[] {
	return buildMarkdownBlocks(tokenize(text), text);
}

/** Concatenate the visible text of an inline segment list (markers excluded). */
function inlineText(inline: InlineSegment[]): string {
	return inline
		.map((s) => {
			if (s.kind === 'image') return '';
			return s.text;
		})
		.join('');
}

/** Collect the word segments (interactive tokens) of an inline list. */
function words(inline: InlineSegment[]): { text: string; index: number; marks: string[] }[] {
	return inline
		.filter((s): s is Extract<InlineSegment, { kind: 'word' }> => s.kind === 'word')
		.map((s) => ({ text: s.text, index: s.index, marks: s.marks }));
}

describe('buildMarkdownBlocks', () => {
	it('parses an ATX heading and keeps words as interactive tokens', () => {
		const blocks = build('# Hello World\n\nA paragraph.');
		expect(blocks[0]).toMatchObject({ kind: 'heading', level: 1 });
		const heading = blocks[0] as Extract<Block, { kind: 'heading' }>;
		expect(words(heading.inline).map((w) => w.text)).toEqual(['Hello', 'World']);
		// Token indices are preserved from the global token stream.
		expect(words(heading.inline).map((w) => w.index)).toEqual([0, 1]);
		expect(blocks[1]).toMatchObject({ kind: 'paragraph' });
	});

	it('derives heading level from the number of hashes', () => {
		const blocks = build('### Deep');
		expect(blocks[0]).toMatchObject({ kind: 'heading', level: 3 });
	});

	it('renders bold and italic as marks while hiding the markers', () => {
		const blocks = build('This is **bold** and *italic* text.');
		const p = blocks[0] as Extract<Block, { kind: 'paragraph' }>;
		const bold = words(p.inline).find((w) => w.text === 'bold');
		const italic = words(p.inline).find((w) => w.text === 'italic');
		expect(bold?.marks).toEqual(['strong']);
		expect(italic?.marks).toEqual(['em']);
		// Marker asterisks are not part of the visible text.
		expect(inlineText(p.inline)).not.toContain('*');
	});

	it('renders inline code as a code mark', () => {
		const blocks = build('Call `fetch` now.');
		const p = blocks[0] as Extract<Block, { kind: 'paragraph' }>;
		const code = words(p.inline).find((w) => w.text === 'fetch');
		expect(code?.marks).toEqual(['code']);
		expect(inlineText(p.inline)).not.toContain('`');
	});

	it('keeps intraword underscores (snake_case) literal', () => {
		const blocks = build('The value is read_only here.');
		const p = blocks[0] as Extract<Block, { kind: 'paragraph' }>;
		// The underscore is not an emphasis delimiter: it survives verbatim and no
		// word carries an `em` mark.
		expect(inlineText(p.inline)).toContain('read_only');
		expect(words(p.inline).every((w) => !w.marks.includes('em'))).toBe(true);
	});

	it('parses a bullet list into items', () => {
		const blocks = build('- milk\n- eggs\n- bread');
		const list = blocks[0] as Extract<Block, { kind: 'list' }>;
		expect(list.kind).toBe('list');
		expect(list.ordered).toBe(false);
		expect(list.items.map((it) => inlineText(it).trim())).toEqual(['milk', 'eggs', 'bread']);
	});

	it('parses an ordered list', () => {
		const blocks = build('1. first\n2. second');
		const list = blocks[0] as Extract<Block, { kind: 'list' }>;
		expect(list.ordered).toBe(true);
		expect(list.items).toHaveLength(2);
	});

	it('parses a blockquote and strips the marker', () => {
		const blocks = build('> quoted wisdom');
		const bq = blocks[0] as Extract<Block, { kind: 'blockquote' }>;
		expect(bq.kind).toBe('blockquote');
		expect(inlineText(bq.inline).trim()).toBe('quoted wisdom');
		expect(words(bq.inline).map((w) => w.text)).toEqual(['quoted', 'wisdom']);
	});

	it('parses a fenced code block as non-interactive raw text', () => {
		const blocks = build('```\nconst x = 1;\n```');
		const code = blocks[0] as Extract<Block, { kind: 'code' }>;
		expect(code.kind).toBe('code');
		expect(code.text).toBe('const x = 1;');
	});

	it('parses a thematic break', () => {
		const blocks = build('a\n\n---\n\nb');
		expect(blocks.some((b) => b.kind === 'hr')).toBe(true);
	});

	it('parses a table with header and body cells', () => {
		const blocks = build('| Name | Age |\n| --- | --- |\n| Ann | 30 |');
		const table = blocks[0] as Extract<Block, { kind: 'table' }>;
		expect(table.kind).toBe('table');
		expect(table.header.map((c) => inlineText(c).trim())).toEqual(['Name', 'Age']);
		expect(table.rows).toHaveLength(1);
		expect(table.rows[0].map((c) => inlineText(c).trim())).toEqual(['Ann', '30']);
	});

	it('renders markdown links as link segments', () => {
		const blocks = build('See [the docs](https://example.com/docs) now.');
		const p = blocks[0] as Extract<Block, { kind: 'paragraph' }>;
		const link = p.inline.find((s) => s.kind === 'link');
		expect(link).toMatchObject({ kind: 'link', url: 'https://example.com/docs' });
	});

	it('merges soft-wrapped lines into one paragraph', () => {
		const blocks = build('line one\nline two\n\nnext para');
		expect(blocks).toHaveLength(2);
		expect(blocks[0].kind).toBe('paragraph');
		expect(blocks[1].kind).toBe('paragraph');
	});

	it('preserves global token indices across blocks', () => {
		const blocks = build('# Title\n\nbody word');
		const heading = blocks[0] as Extract<Block, { kind: 'heading' }>;
		const para = blocks[1] as Extract<Block, { kind: 'paragraph' }>;
		const headingIdx = words(heading.inline).map((w) => w.index);
		const paraIdx = words(para.inline).map((w) => w.index);
		// Indices are strictly increasing and unique across the document.
		expect(headingIdx).toEqual([0]);
		expect(paraIdx).toEqual([1, 2]);
	});
});
