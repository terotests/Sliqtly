---
title: Vega-Lite-kaaviotyypit
transition: fade
---

# Vega-Lite-kaaviotyypit

Jokainen dia on yksi Vega-Lite-esimerkki. Tiedostoista luetut aineistot (`"url": "data/…"`) haetaan Vegan esimerkkiaineistoista.
{.lead}

## Pylväs

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

## Vaakapylväs

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

## Ryhmitetty pylväs

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

## Pinottu pylväs

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

## Histogrammi

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

## Viiva, monta sarjaa

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

## Pehmeä viiva pisteillä

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

## Porrasviiva

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

## Pinottu alue

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

## Hajontakaavio

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

## Kuplakaavio

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

## Lämpökartta (binnattu)

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
  "width": 300,
  "height": 200,
  "encoding": {
    "x": {"bin": {"maxbins": 60}, "field": "IMDB Rating", "type": "quantitative"},
    "y": {"bin": {"maxbins": 40}, "field": "Rotten Tomatoes Rating", "type": "quantitative"},
    "color": {"aggregate": "count", "type": "quantitative"}
  },
  "config": {"view": {"stroke": "transparent"}}
}
```

## Lämpökartta tekstillä

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

## Piirakka

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

## Donitsi

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

## Laatikkokuvio

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

## Virhepalkit

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

## Viivakoodi (tick)

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

## Pylväs ja keskiarvoviiva

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

## Pienet monikot (facet)

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

## Aikajana: lämpötila

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
