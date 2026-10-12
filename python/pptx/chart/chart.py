"""The chart object model; drawn as a Vega-Lite spec by pptx._markdown."""

from pptx.dml.fill import FillFormat
from pptx.dml.line import LineFormat
from pptx.text.text import Font, TextFrame


class _Format:
    def __init__(self):
        self.fill = FillFormat()
        self.line = LineFormat()


class ChartTitle:
    def __init__(self):
        self._text_frame = None
        self.format = _Format()

    @property
    def has_text_frame(self):
        return self._text_frame is not None

    @has_text_frame.setter
    def has_text_frame(self, value):
        self._text_frame = TextFrame() if value else None

    @property
    def text_frame(self):
        if self._text_frame is None:
            self._text_frame = TextFrame()
        return self._text_frame

    def _text(self):
        return self._text_frame.text.strip() if self._text_frame is not None else ""


class AxisTitle(ChartTitle):
    pass


class TickLabels:
    def __init__(self):
        self.font = Font()
        self.number_format = "General"
        self.number_format_is_linked = True
        self.offset = 100


class _Axis:
    def __init__(self):
        self.has_major_gridlines = False
        self.has_minor_gridlines = False
        self.maximum_scale = None
        self.minimum_scale = None
        self.major_unit = None
        self.minor_unit = None
        self.visible = True
        self.reverse_order = False
        self.tick_labels = TickLabels()
        self.tick_label_position = None
        self.major_tick_mark = None
        self.minor_tick_mark = None
        self.format = _Format()
        self._title = None

    @property
    def has_title(self):
        return self._title is not None

    @has_title.setter
    def has_title(self, value):
        self._title = AxisTitle() if value else None

    @property
    def axis_title(self):
        if self._title is None:
            self._title = AxisTitle()
        return self._title

    @property
    def major_gridlines(self):
        self.has_major_gridlines = True
        return _Format()


class CategoryAxis(_Axis):
    pass


class ValueAxis(_Axis):
    def __init__(self):
        _Axis.__init__(self)
        self.has_major_gridlines = True


class DataLabels:
    def __init__(self):
        self.font = Font()
        self.number_format = "General"
        self.number_format_is_linked = True
        self.position = None
        self.show_value = True
        self.show_percentage = False
        self.show_category_name = False
        self.show_series_name = False
        self.show_legend_key = False


class Legend:
    def __init__(self):
        self.position = None
        self.include_in_layout = True
        self.horz_offset = 0.0
        self.font = Font()


class Marker:
    def __init__(self):
        self.style = None
        self.size = None
        self.format = _Format()


class Point:
    def __init__(self):
        self.format = _Format()
        self.data_label = DataLabels()
        self.marker = Marker()


class Series:
    def __init__(self, chart, index, name):
        self._chart = chart
        self.index = index
        self.name = name
        self.format = _Format()
        self.smooth = False
        self.marker = Marker()
        self.invert_if_negative = True
        self._points = {}
        self._data_labels = None

    @property
    def values(self):
        return tuple(self._chart._data[self.index].values)

    @property
    def points(self):
        return _Points(self)

    @property
    def data_labels(self):
        if self._data_labels is None:
            self._data_labels = DataLabels()
        return self._data_labels


class _Points:
    def __init__(self, series):
        self._series = series

    def __getitem__(self, i):
        return self._series._points.setdefault(i, Point())

    def __len__(self):
        return len(self._series.values)


class _Plot:
    def __init__(self, chart):
        self._chart = chart
        self.has_data_labels = False
        self._data_labels = None
        self.gap_width = 150
        self.overlap = 0
        self.vary_by_categories = True

    @property
    def data_labels(self):
        if self._data_labels is None:
            self._data_labels = DataLabels()
        return self._data_labels

    @property
    def series(self):
        return self._chart.series

    @property
    def categories(self):
        return tuple(c.label for c in self._chart._data.categories)


class Chart:
    def __init__(self, chart_type, chart_data):
        self.chart_type = chart_type
        self.has_legend = False
        self.legend = Legend()
        self._has_title = None
        self.chart_title = ChartTitle()
        self.font = Font()
        self.chart_style = None
        self.category_axis = CategoryAxis()
        self.value_axis = ValueAxis()
        self.replace_data(chart_data)

    def replace_data(self, chart_data):
        self._data = chart_data
        self.series = [Series(self, i, s.name) for i, s in enumerate(chart_data)]
        self.plots = [_Plot(self)]
        # python-pptx puts a legend on a chart of more than one series
        if len(self.series) > 1:
            self.has_legend = True

    @property
    def has_title(self):
        if self._has_title is None:
            return self.chart_title._text() != ""
        return self._has_title

    @has_title.setter
    def has_title(self, value):
        self._has_title = bool(value)
