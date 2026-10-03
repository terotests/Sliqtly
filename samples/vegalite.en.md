---
title: Vega-Lite chart types
transition: fade
---

# Vega-Lite chart types

Each slide is one Vega-Lite example. Data read from files (`"url": "data/…"`) comes from Vega's example datasets.
{.lead}

## Bar

```vega-lite
{
  "data": {"values": [
    {"a": "A", "b": 28}, {"a": "B", "b": 55}, {"a": "C", "b": 43},
    {"a": "D", "b": 91}, {"a": "E", "b": 81}, {"a": "F", "b": 53},
    {"a": "G", "b": 19}, {"a": "H", "b": 87}, {"a": "I", "b": 52}
  ]},
  "mark": "bar",
  "width": 560,
  "encoding": {
    "x": {"field": "a", "type": "nominal", "axis": {"labelAngle": 0}},
    "y": {"field": "b", "type": "quantitative"}
  }
}
```

## Horizontal bar

```vega-lite
{
  "data": {"url": "data/population.json"},
  "transform": [{"filter": "datum.year == 2000"}],
  "mark": "bar",
  "width": 520,
  "encoding": {
    "y": {"field": "age", "type": "ordinal"},
    "x": {"aggregate": "sum", "field": "people", "title": "population"}
  }
}
```

## Grouped bar

```vega-lite
{
  "data": {"values": [
    {"category": "A", "group": "x", "value": 0.1},
    {"category": "A", "group": "y", "value": 0.6},
    {"category": "A", "group": "z", "value": 0.9},
    {"category": "B", "group": "x", "value": 0.7},
    {"category": "B", "group": "y", "value": 0.2},
    {"category": "B", "group": "z", "value": 1.1},
    {"category": "C", "group": "x", "value": 0.6},
    {"category": "C", "group": "y", "value": 0.1},
    {"category": "C", "group": "z", "value": 0.2}
  ]},
  "mark": "bar",
  "width": 520,
  "encoding": {
    "x": {"field": "category"},
    "y": {"field": "value", "type": "quantitative"},
    "xOffset": {"field": "group"},
    "color": {"field": "group"}
  }
}
```

## Stacked bar

```vega-lite
{
  "data": {"url": "data/seattle-weather.csv"},
  "mark": "bar",
  "width": 560,
  "encoding": {
    "x": {"timeUnit": "month", "field": "date", "type": "ordinal", "title": "Month of the year"},
    "y": {"aggregate": "count", "type": "quantitative"},
    "color": {
      "field": "weather",
      "type": "nominal",
      "scale": {
        "domain": ["sun", "fog", "drizzle", "rain", "snow"],
        "range": ["#e7ba52", "#c7c7c7", "#aec7e8", "#1f77b4", "#9467bd"]
      },
      "title": "Weather type"
    }
  }
}
```

## Histogram

```vega-lite
{
  "data": {"url": "data/movies.json"},
  "mark": "bar",
  "width": 560,
  "encoding": {
    "x": {"bin": true, "field": "IMDB Rating"},
    "y": {"aggregate": "count"}
  }
}
```

## Line, several series

```vega-lite
{
  "data": {"url": "data/stocks.csv"},
  "mark": "line",
  "width": 560,
  "encoding": {
    "x": {"field": "date", "type": "temporal"},
    "y": {"field": "price", "type": "quantitative"},
    "color": {"field": "symbol", "type": "nominal"}
  }
}
```

## Smooth line with points

```vega-lite
{
  "data": {"values": [
    {"x": 1, "y": 28}, {"x": 2, "y": 55}, {"x": 3, "y": 43}, {"x": 4, "y": 91},
    {"x": 5, "y": 81}, {"x": 6, "y": 53}, {"x": 7, "y": 19}, {"x": 8, "y": 87}
  ]},
  "mark": {"type": "line", "interpolate": "monotone", "point": {"filled": false, "fill": "white"}},
  "width": 560,
  "encoding": {
    "x": {"field": "x", "type": "quantitative"},
    "y": {"field": "y", "type": "quantitative"}
  }
}
```

## Step line

```vega-lite
{
  "data": {"url": "data/stocks.csv"},
  "transform": [{"filter": "datum.symbol === 'GOOG'"}],
  "mark": {"type": "line", "interpolate": "step-after"},
  "width": 560,
  "encoding": {
    "x": {"field": "date", "type": "temporal"},
    "y": {"field": "price", "type": "quantitative"}
  }
}
```

## Stacked area

```vega-lite
{
  "data": {"url": "data/unemployment-across-industries.json"},
  "mark": "area",
  "width": 560,
  "encoding": {
    "x": {"timeUnit": "yearmonth", "field": "date", "axis": {"format": "%Y"}},
    "y": {"aggregate": "sum", "field": "count"},
    "color": {"field": "series", "scale": {"scheme": "category20b"}}
  }
}
```

## Scatter plot

```vega-lite
{
  "data": {"url": "data/cars.json"},
  "mark": "point",
  "width": 520,
  "encoding": {
    "x": {"field": "Horsepower", "type": "quantitative"},
    "y": {"field": "Miles_per_Gallon", "type": "quantitative"},
    "color": {"field": "Origin", "type": "nominal"},
    "shape": {"field": "Origin", "type": "nominal"}
  }
}
```

## Bubble chart

```vega-lite
{
  "data": {"url": "data/cars.json"},
  "mark": "circle",
  "width": 520,
  "encoding": {
    "x": {"field": "Horsepower", "type": "quantitative"},
    "y": {"field": "Miles_per_Gallon", "type": "quantitative"},
    "size": {"field": "Acceleration", "type": "quantitative"},
    "opacity": {"value": 0.6}
  }
}
```

## Heatmap (binned)

```vega-lite
{
  "data": {"url": "data/movies.json"},
  "transform": [{
    "filter": {"and": [
      {"field": "IMDB Rating", "valid": true},
      {"field": "Rotten Tomatoes Rating", "valid": true}
    ]}
  }],
  "mark": "rect",
  "encoding": {
    "x": {"bin": {"maxbins": 60}, "field": "IMDB Rating", "type": "quantitative"},
    "y": {"bin": {"maxbins": 40}, "field": "Rotten Tomatoes Rating", "type": "quantitative"},
    "color": {"aggregate": "count", "type": "quantitative"}
  },
  "config": {"view": {"stroke": "transparent"}}
}
```

## Heatmap with text

```vega-lite
{
  "data": {"url": "data/cars.json"},
  "transform": [{"aggregate": [{"op": "count", "as": "num_cars"}], "groupby": ["Origin", "Cylinders"]}],
  "encoding": {
    "y": {"field": "Origin", "type": "ordinal"},
    "x": {"field": "Cylinders", "type": "ordinal"}
  },
  "layer": [
    {"mark": "rect", "encoding": {"color": {"field": "num_cars", "type": "quantitative", "title": "Count of Records"}}},
    {"mark": "text", "encoding": {
      "text": {"field": "num_cars", "type": "quantitative"},
      "color": {"condition": {"test": "datum['num_cars'] < 40", "value": "black"}, "value": "white"}
    }}
  ],
  "config": {"axis": {"grid": true, "tickBand": "extent"}}
}
```

## Pie

```vega-lite
{
  "data": {"values": [
    {"category": 1, "value": 4}, {"category": 2, "value": 6}, {"category": 3, "value": 10},
    {"category": 4, "value": 3}, {"category": 5, "value": 7}, {"category": 6, "value": 8}
  ]},
  "mark": "arc",
  "encoding": {
    "theta": {"field": "value", "type": "quantitative"},
    "color": {"field": "category", "type": "nominal"}
  }
}
```

## Donut

```vega-lite
{
  "data": {"values": [
    {"category": 1, "value": 4}, {"category": 2, "value": 6}, {"category": 3, "value": 10},
    {"category": 4, "value": 3}, {"category": 5, "value": 7}, {"category": 6, "value": 8}
  ]},
  "mark": {"type": "arc", "innerRadius": 50},
  "encoding": {
    "theta": {"field": "value", "type": "quantitative"},
    "color": {"field": "category", "type": "nominal"}
  }
}
```

## Box plot

```vega-lite
{
  "data": {"url": "data/penguins.json"},
  "mark": {"type": "boxplot", "extent": "min-max"},
  "width": 520,
  "encoding": {
    "x": {"field": "Species", "type": "nominal"},
    "color": {"field": "Species", "type": "nominal", "legend": null},
    "y": {"field": "Body Mass (g)", "type": "quantitative", "scale": {"zero": false}}
  }
}
```

## Error bars

```vega-lite
{
  "data": {"url": "data/barley.json"},
  "encoding": {"y": {"field": "variety", "type": "ordinal"}},
  "width": 520,
  "layer": [
    {"mark": {"type": "point", "filled": true},
     "encoding": {"x": {"aggregate": "mean", "field": "yield", "type": "quantitative", "scale": {"zero": false}, "title": "Barley Yield"}, "color": {"value": "black"}}},
    {"mark": {"type": "errorbar", "extent": "ci"},
     "encoding": {"x": {"field": "yield", "type": "quantitative", "title": "Barley Yield"}}}
  ]
}
```

## Strip plot (tick)

```vega-lite
{
  "data": {"url": "data/cars.json"},
  "mark": "tick",
  "width": 520,
  "encoding": {
    "x": {"field": "Horsepower", "type": "quantitative"},
    "y": {"field": "Cylinders", "type": "ordinal"}
  }
}
```

## Bar with a mean line

```vega-lite
{
  "data": {"url": "data/seattle-weather.csv"},
  "width": 560,
  "layer": [
    {"mark": "bar", "encoding": {
      "x": {"timeUnit": "month", "field": "date", "type": "ordinal"},
      "y": {"aggregate": "mean", "field": "precipitation"}
    }},
    {"mark": "rule", "encoding": {
      "y": {"aggregate": "mean", "field": "precipitation"},
      "color": {"value": "red"},
      "size": {"value": 3}
    }}
  ]
}
```

## Small multiples (facet)

```vega-lite
{
  "data": {"url": "data/cars.json"},
  "mark": "bar",
  "width": 160,
  "height": 160,
  "encoding": {
    "column": {"field": "Origin", "type": "nominal"},
    "x": {"field": "Cylinders", "type": "ordinal"},
    "y": {"aggregate": "count", "type": "quantitative"}
  }
}
```

## Timeline: temperature

```vega-lite
{
  "data": {"url": "data/seattle-weather.csv"},
  "mark": "area",
  "width": 560,
  "encoding": {
    "x": {"timeUnit": "yearmonth", "field": "date", "type": "temporal"},
    "y": {"aggregate": "max", "field": "temp_max", "type": "quantitative"},
    "y2": {"aggregate": "min", "field": "temp_min"}
  }
}
```

## Radial

```vega-lite
{
  "data": {"values": [12, 23, 47, 6, 52, 19]},
  "layer": [
    {"mark": {"type": "arc", "innerRadius": 20, "stroke": "#fff"}},
    {"mark": {"type": "text", "radiusOffset": 10}, "encoding": {"text": {"field": "data", "type": "quantitative"}}}
  ],
  "encoding": {
    "theta": {"field": "data", "type": "quantitative", "stack": true},
    "radius": {"field": "data", "scale": {"type": "sqrt", "zero": true, "rangeMin": 20}},
    "color": {"field": "data", "type": "nominal", "legend": null}
  }
}
```

## Normalized stacked bar

```vega-lite
{
  "data": {"url": "data/barley.json"},
  "mark": "bar",
  "width": 520,
  "encoding": {
    "y": {"field": "site", "type": "nominal"},
    "x": {"aggregate": "sum", "field": "yield", "stack": "normalize"},
    "color": {"field": "variety", "type": "nominal"}
  }
}
```

## Diverging bar (negatives)

```vega-lite
{
  "data": {"values": [
    {"a": "A", "b": -28}, {"a": "B", "b": 55}, {"a": "C", "b": -33},
    {"a": "D", "b": 91}, {"a": "E", "b": 81}, {"a": "F", "b": 53},
    {"a": "G", "b": -19}, {"a": "H", "b": 87}, {"a": "I", "b": 52}
  ]},
  "mark": "bar",
  "width": 560,
  "encoding": {
    "x": {"field": "a", "type": "nominal", "axis": {"labelAngle": 0}},
    "y": {"field": "b", "type": "quantitative"},
    "color": {"condition": {"test": "datum.b < 0", "value": "#e45756"}, "value": "#4c78a8"}
  }
}
```

## Gantt (timeline)

```vega-lite
{
  "data": {"values": [
    {"task": "Suunnittelu", "start": 1, "end": 3},
    {"task": "Toteutus", "start": 3, "end": 8},
    {"task": "Testaus", "start": 6, "end": 9},
    {"task": "Julkaisu", "start": 9, "end": 10}
  ]},
  "mark": "bar",
  "width": 520,
  "encoding": {
    "y": {"field": "task", "type": "ordinal", "sort": null},
    "x": {"field": "start", "type": "quantitative", "title": "viikko"},
    "x2": {"field": "end"}
  }
}
```

## Lollipop

```vega-lite
{
  "data": {"values": [
    {"k": "A", "v": 28}, {"k": "B", "v": 55}, {"k": "C", "v": 43},
    {"k": "D", "v": 91}, {"k": "E", "v": 81}, {"k": "F", "v": 53}
  ]},
  "width": 520,
  "encoding": {
    "y": {"field": "k", "type": "nominal"},
    "x": {"field": "v", "type": "quantitative"}
  },
  "layer": [
    {"mark": "rule", "encoding": {"x2": {"datum": 0}}},
    {"mark": {"type": "circle", "size": 160}}
  ]
}
```

## Candlestick

```vega-lite
{
  "data": {"url": "data/ohlc.json"},
  "width": 560,
  "encoding": {
    "x": {"field": "date", "type": "temporal", "title": "Date in 2009", "axis": {"format": "%m/%d"}},
    "y": {"type": "quantitative", "scale": {"zero": false}, "title": "Price"},
    "color": {"condition": {"test": "datum.open < datum.close", "value": "#06982d"}, "value": "#ae1325"}
  },
  "layer": [
    {"mark": "rule", "encoding": {"y": {"field": "low"}, "y2": {"field": "high"}}},
    {"mark": "bar", "encoding": {"y": {"field": "open"}, "y2": {"field": "close"}}}
  ]
}
```

## Regression line

```vega-lite
{
  "data": {"url": "data/movies.json"},
  "width": 520,
  "layer": [
    {"mark": {"type": "point", "filled": true, "opacity": 0.3},
     "encoding": {"x": {"field": "Rotten Tomatoes Rating", "type": "quantitative"}, "y": {"field": "IMDB Rating", "type": "quantitative"}}},
    {"mark": {"type": "line", "color": "firebrick", "strokeWidth": 3},
     "transform": [{"regression": "IMDB Rating", "on": "Rotten Tomatoes Rating"}],
     "encoding": {"x": {"field": "Rotten Tomatoes Rating", "type": "quantitative"}, "y": {"field": "IMDB Rating", "type": "quantitative"}}}
  ]
}
```

## Density

```vega-lite
{
  "data": {"url": "data/movies.json"},
  "width": 520,
  "transform": [{"density": "IMDB Rating", "bandwidth": 0.3}],
  "mark": "area",
  "encoding": {
    "x": {"field": "value", "type": "quantitative", "title": "IMDB Rating"},
    "y": {"field": "density", "type": "quantitative"}
  }
}
```

## Error band

```vega-lite
{
  "data": {"url": "data/cars.json"},
  "width": 520,
  "encoding": {"x": {"field": "Year", "timeUnit": "year"}},
  "layer": [
    {"mark": {"type": "errorband", "extent": "ci"},
     "encoding": {"y": {"field": "Miles_per_Gallon", "type": "quantitative", "title": "Mean of Miles per Gallon (95% CIs)"}}},
    {"mark": "line", "encoding": {"y": {"aggregate": "mean", "field": "Miles_per_Gallon"}}}
  ]
}
```

## Slope

```vega-lite
{
  "data": {"url": "data/barley.json"},
  "width": 300,
  "mark": "line",
  "encoding": {
    "x": {"field": "year", "type": "ordinal", "scale": {"padding": 0.5}},
    "y": {"aggregate": "median", "field": "yield", "type": "quantitative"},
    "color": {"field": "site", "type": "nominal"}
  }
}
```

## Bump

```vega-lite
{
  "data": {"values": [
    {"build": 1, "result": "PASSED"}, {"build": 2, "result": "PASSED"}, {"build": 3, "result": "FAILED"},
    {"build": 4, "result": "FAILED"}, {"build": 5, "result": "SKIPPED"}, {"build": 6, "result": "PASSED"},
    {"build": 7, "result": "PASSED"}, {"build": 8, "result": "FAILED"}, {"build": 9, "result": "SKIPPED"}
  ]},
  "width": 520,
  "mark": {"type": "line", "point": true},
  "encoding": {
    "x": {"field": "build", "type": "ordinal"},
    "y": {"field": "result", "type": "nominal", "sort": ["PASSED", "SKIPPED", "FAILED"]}
  }
}
```

## Binned scatter (circles)

```vega-lite
{
  "data": {"url": "data/movies.json"},
  "mark": "circle",
  "width": 520,
  "encoding": {
    "x": {"bin": {"maxbins": 10}, "field": "IMDB Rating"},
    "y": {"bin": {"maxbins": 10}, "field": "Rotten Tomatoes Rating"},
    "size": {"aggregate": "count"}
  }
}
```

## Side by side (hconcat)

```vega-lite
{
  "data": {"url": "data/weather.csv"},
  "transform": [{"filter": "datum.location === 'Seattle'"}],
  "hconcat": [
    {"mark": "bar", "encoding": {"x": {"timeUnit": "month", "field": "date", "type": "ordinal"}, "y": {"aggregate": "mean", "field": "precipitation"}}},
    {"mark": "point", "encoding": {"x": {"field": "temp_min", "bin": true}, "y": {"field": "temp_max", "bin": true}, "size": {"aggregate": "count"}}}
  ]
}
```

## Repeat

```vega-lite
{
  "data": {"url": "data/cars.json"},
  "repeat": ["Horsepower", "Miles_per_Gallon", "Acceleration"],
  "columns": 3,
  "spec": {
    "width": 160,
    "height": 140,
    "mark": "bar",
    "encoding": {
      "x": {"field": {"repeat": "repeat"}, "bin": true},
      "y": {"aggregate": "count"},
      "color": {"field": "Origin"}
    }
  }
}
```

## Waterfall

```vega-lite
{
  "data": {"values": [
    {"label": "Alku", "amount": 4000},
    {"label": "Tammi", "amount": 1707}, {"label": "Helmi", "amount": -1425},
    {"label": "Maalis", "amount": -1030}, {"label": "Huhti", "amount": 1812},
    {"label": "Touko", "amount": -1067}, {"label": "Loppu", "amount": 0}
  ]},
  "width": 560,
  "transform": [
    {"window": [{"op": "sum", "field": "amount", "as": "sum"}]},
    {"window": [{"op": "lead", "field": "label", "as": "lead"}]},
    {"calculate": "datum.lead === null ? datum.label : datum.lead", "as": "lead"},
    {"calculate": "datum.label === 'Loppu' ? 0 : datum.sum - datum.amount", "as": "previous_sum"},
    {"calculate": "datum.label === 'Loppu' ? datum.sum : datum.amount", "as": "amount"},
    {"calculate": "(datum.label !== 'Alku' && datum.label !== 'Loppu' && datum.amount > 0 ? '+' : '') + datum.amount", "as": "text_amount"},
    {"calculate": "(datum.sum + datum.previous_sum) / 2", "as": "center"}
  ],
  "encoding": {"x": {"field": "label", "type": "ordinal", "sort": null, "axis": {"labelAngle": 0}}},
  "layer": [
    {"mark": {"type": "bar", "size": 45},
     "encoding": {
       "y": {"field": "previous_sum", "type": "quantitative", "title": "Saldo"},
       "y2": {"field": "sum"},
       "color": {"condition": [
         {"test": "datum.label === 'Alku' || datum.label === 'Loppu'", "value": "#f7e0b6"},
         {"test": "datum.sum < datum.previous_sum", "value": "#f78a64"}
       ], "value": "#93c4aa"}
     }},
    {"mark": {"type": "text", "fontWeight": "bold", "baseline": "middle"},
     "encoding": {"y": {"field": "center", "type": "quantitative"}, "text": {"field": "text_amount", "type": "nominal"}}}
  ]
}
```

## Map (points)

```vega-lite
{
  "width": 560,
  "height": 320,
  "data": {"url": "data/airports.csv"},
  "projection": {"type": "albersUsa"},
  "mark": {"type": "circle", "size": 10, "opacity": 0.7},
  "encoding": {
    "longitude": {"field": "longitude", "type": "quantitative"},
    "latitude": {"field": "latitude", "type": "quantitative"}
  }
}
```

## Text table

```vega-lite
{
  "data": {"url": "data/cars.json"},
  "transform": [{"aggregate": [{"op": "mean", "field": "Horsepower", "as": "hp"}], "groupby": ["Origin", "Cylinders"]}],
  "mark": {"type": "text", "fontSize": 16},
  "width": 420,
  "encoding": {
    "y": {"field": "Origin", "type": "nominal"},
    "x": {"field": "Cylinders", "type": "ordinal"},
    "text": {"field": "hp", "type": "quantitative", "format": ".0f"}
  }
}
```
