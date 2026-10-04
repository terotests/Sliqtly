---
title: Sliqtly - Demo
transition: fade
---
# Sliqtly {bg=media/bg.png fx=raindrops2}

![Sliqtly](media/logo.svg)
{width=100% height=100%}

## Publishing process

```mermaid
flowchart LR
  A[Rectangle] --> B(Rounded) --> C([Stadium])
  D[(Database)] --> E((Circle)) --> F{Decision}
  G{{Hexagon}} --> H[/Parallelogram/] --> I[[Subroutine]]
```

## Sales by region

```vega-lite
{"$schema": "https://vega.github.io/schema/vega-lite/v5.json", "background": "rgba(0,0,0,0)", "config": {"axis": {"labelColor": "#c8d0f0", "titleColor": "#c8d0f0", "gridColor": "#2d3a7a", "domainColor": "#5c6aa8", "tickColor": "#5c6aa8", "labelFontSize": 14}, "legend": {"labelColor": "#c8d0f0", "labelFontSize": 14}, "view": {"stroke": null}}, "width": 820, "height": 320, "data": {"values": [{"q": "Q1", "region": "South", "k": 460}, {"q": "Q1", "region": "West", "k": 384}, {"q": "Q1", "region": "East", "k": 280}, {"q": "Q1", "region": "North", "k": 268}, {"q": "Q2", "region": "South", "k": 512}, {"q": "Q2", "region": "West", "k": 434}, {"q": "Q2", "region": "East", "k": 344}, {"q": "Q2", "region": "North", "k": 312}, {"q": "Q3", "region": "South", "k": 539}, {"q": "Q3", "region": "West", "k": 457}, {"q": "Q3", "region": "East", "k": 379}, {"q": "Q3", "region": "North", "k": 313}, {"q": "Q4", "region": "South", "k": 604}, {"q": "Q4", "region": "West", "k": 502}, {"q": "Q4", "region": "East", "k": 416}, {"q": "Q4", "region": "North", "k": 315}]}, "mark": {"type": "bar", "cornerRadiusEnd": 3}, "encoding": {"x": {"field": "q", "type": "ordinal", "title": null, "axis": {"labelAngle": 0}}, "xOffset": {"field": "region"}, "y": {"field": "k", "type": "quantitative", "title": "k€"}, "color": {"field": "region", "type": "nominal", "title": null, "legend": {"orient": "top"}}, "tooltip": [{"field": "region"}, {"field": "q"}, {"field": "k"}]}}
```

## One source, many formats

![Sliqtly](media/radial.xml)
{layout=radial1 colors=colorful1}

## Roadmap

```timeline
- Q1: Markdown editor
- Q2: Charts and flowcharts
- Q3: MCP server
- Q4: Enterprise server
```

## Architecture

```mermaid
flowchart LR
  subgraph Client
    U[User] --> W[Browser]
  end
  subgraph Server
    A[API] --> D[(Database)]
    A --> Q[Queue]
  end
  W --> A
  Q --> X[Worker]
  classDef hot fill:#ffa546,stroke:#b96926,color:#111
  classDef cool fill:#5ce1ff,stroke:#2b8fa8,color:#111
  class A,Q hot
  class D cool
  style X stroke-dasharray: 5 5
```

## Math Symbols

$$f'(x) = \lim_{h \to 0} \frac{f(x+h) - f(x)}{h}$$

$$\int_0^\infty e^{-x^2}\,dx = \frac{\sqrt{\pi}}{2}$$

# Layouts and line art {art=waves}

- Plan: goals, budget and schedule
- Build: code, content and tests
- Pilot: ten customers try it
- Launch: open to everyone
{list-style=process}

{.lead}

::: notes
The line art is a PRO feature; the layouts are free. Every layout below is computed from the text and the room on the slide; change the theme and they change colour with it.
:::


## Code and Syntax

```typescript
const deck = await sliqtly.create({
  title: "Quarterly review",
  theme: "aurora",
  markdown: "# Q4\n\n## Sales\n...",
});
console.log(deck.share_url);
```

## Effects { fx=starfield }

```mermaid
flowchart LR
  A[Run done] --> B{Any pain?}
  B -->|No| C[Stretch and recover]
  B -->|Yes| D{Worse when walking?}
  D -->|No| E[Go easier next time]
  D -->|Yes| F[Rest and ask a physio]
  E --> A
  C --> A
```

