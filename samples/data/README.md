# Sample chart data

The files the sample decks' charts read (`"data": {"url": "data/…"}`), copied
unchanged from [vega-datasets](https://github.com/vega/vega-datasets) 2.11.0
(BSD-3-Clause; each dataset's own source is listed in that repository's
SOURCES.md). The build copies them to `web/dist/data/`, beside the page, which
is where a chart looks for a relative file first (web/main.js
fetchChartFiles). `web/test/sample-data.test.mjs` checks that every such URL in
`samples/*.md` has its file here.
