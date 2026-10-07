# Sliqtly vs Claude's PowerPoint skill: how the front page's table was measured

2026-10-07. The same 15-slide deck (title with key figures, agenda, 6 charts,
2 tables, a flowchart, SWOT, two columns, a timeline, next-step cards) made
both ways:

- `deck.md`: the Sliqtly Markdown, created with `create_presentation`
  (theme `corporate`) on a local server built from main.
- `deck-pptxgenjs.js`: the script an agent writes following the PowerPoint
  skill's SKILL.md (pptxgenjs, structured deck: theme, layouts, placeholders,
  native charts). It builds and passes the skill's `validate.py`.
- `edit.json`: a later change (September's revenue and the lead under it).

Token counts: `@anthropic-ai/tokenizer` (Anthropic's public tokenizer; an
approximation for current models, so read the ratios, not the exact numbers).

| | tokens | bytes |
| --- | ---: | ---: |
| Read first: `sliqtly_guide` | 11 168 | 38 590 |
| Read first: Sliqtly MCP tool definitions (34 tools, every request) | 10 681 | 41 661 |
| Read first: PowerPoint skill SKILL.md | 7 640 | 28 618 |
| Written: Sliqtly `create_presentation` call | 1 555 | 4 671 |
| Written: pptxgenjs script | 4 917 | 12 851 |
| Returned: Sliqtly layout report after create | 1 570 | 5 161 |
| Read back later: `get_presentation` | 1 389 | 4 198 |
| Read back later: `markitdown deck.pptx` (text only, no chart data) | 1 111 | 3 212 |
| Read back later: the 6 charts' XML in the .pptx | 12 354 | 28 286 |
| Written: Sliqtly `edits` change | 68 | 211 |
| Returned: Sliqtly report after the edit | 1 652 | 5 383 |

Files: `deck.md` 4 030 bytes, `deck.pptx` 91 249 bytes.

What this means: Sliqtly writes about a third of the tokens and changes a
deck with a small edit, but its guide and tool definitions are about three
times the PowerPoint skill's instructions, and each create/update returns a
layout report of about 1 500 tokens. For one new deck the two cost about the
same (output tokens cost more than input); the difference grows with more
slides and with every later change, and prompt caching makes the fixed part
cheaper.

Speed: about the same. Timings varied between tests (one run PowerPoint
2.3 min / 1 round, Sliqtly 4.8 min / 4 fix rounds; other runs Sliqtly was
faster). The Google Slides numbers (about 5 200 tokens written, about 100
API requests for 6 slides) come from the earlier tests; the fixes the
Sliqtly run showed were merged on 2026-10-06 (Sliqtly #287, #289).
