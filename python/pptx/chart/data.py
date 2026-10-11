"""Chart data as python-pptx's chart.data has it."""


class Categories(list):
    @property
    def depth(self):
        return 1

    def add_category(self, label):
        c = Category(label)
        self.append(c)
        return c


class Category:
    def __init__(self, label):
        self.label = label
        self.sub_categories = []

    def add_sub_category(self, label):
        c = Category(label)
        self.sub_categories.append(c)
        return c

    def __str__(self):
        return str(self.label)


class _Series:
    def __init__(self, chart_data, name, number_format=None):
        self._chart_data = chart_data
        self.name = name
        self._number_format = number_format

    @property
    def number_format(self):
        return self._number_format or self._chart_data.number_format


class CategorySeriesData(_Series):
    def __init__(self, chart_data, name, values, number_format=None):
        _Series.__init__(self, chart_data, name, number_format)
        self._values = list(values)

    @property
    def values(self):
        return tuple(self._values)

    def add_data_point(self, value, number_format=None):
        self._values.append(value)

    @property
    def categories(self):
        return self._chart_data.categories


class CategoryChartData:
    def __init__(self, number_format="General"):
        self.number_format = number_format
        self._categories = Categories()
        self._series = []

    @property
    def categories(self):
        return self._categories

    @categories.setter
    def categories(self, labels):
        self._categories = Categories()
        for label in labels:
            self._categories.add_category(label)

    def add_category(self, label):
        return self._categories.add_category(label)

    def add_series(self, name, values=(), number_format=None):
        s = CategorySeriesData(self, name, values, number_format)
        self._series.append(s)
        return s

    def __iter__(self):
        return iter(self._series)

    def __len__(self):
        return len(self._series)

    def __getitem__(self, i):
        return self._series[i]


ChartData = CategoryChartData


class XySeriesData(_Series):
    def __init__(self, chart_data, name, number_format=None):
        _Series.__init__(self, chart_data, name, number_format)
        self.points = []

    def add_data_point(self, x, y, number_format=None):
        self.points.append((x, y))

    @property
    def x_values(self):
        return tuple(p[0] for p in self.points)

    @property
    def y_values(self):
        return tuple(p[1] for p in self.points)


class BubbleSeriesData(XySeriesData):
    def add_data_point(self, x, y, size, number_format=None):
        self.points.append((x, y, size))

    @property
    def bubble_sizes(self):
        return tuple(p[2] for p in self.points)


class XyChartData:
    _series_class = XySeriesData

    def __init__(self, number_format="General"):
        self.number_format = number_format
        self._series = []

    def add_series(self, name, number_format=None):
        s = self._series_class(self, name, number_format)
        self._series.append(s)
        return s

    def __iter__(self):
        return iter(self._series)

    def __len__(self):
        return len(self._series)

    def __getitem__(self, i):
        return self._series[i]


class BubbleChartData(XyChartData):
    _series_class = BubbleSeriesData
