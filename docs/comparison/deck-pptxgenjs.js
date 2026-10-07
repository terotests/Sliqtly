const pptxgen = require("pptxgenjs");
const { applyTheme } = require("/mnt/skills/public/pptx/scripts/apply_theme.js");

const THEME = {
  name: "Northwind",
  headFontFace: "Calibri",
  bodyFontFace: "Calibri",
  colors: {
    dk1: "1F2937", lt1: "FFFFFF", dk2: "1F4E79", lt2: "EEF3F9",
    accent1: "2E5C8A", accent2: "5B9BD5", accent3: "A5B8D0", accent4: "1F7A8C",
    accent5: "E07A5F", accent6: "81B29A", hlink: "2E5C8A", folHlink: "5B9BD5",
  },
};
const HEX = THEME.colors;

const pres = new pptxgen();
pres.layout = "LAYOUT_WIDE";
pres.title = "Customer review 2026";
pres.theme = { headFontFace: THEME.headFontFace, bodyFontFace: THEME.bodyFontFace };
const C = pres.SchemeColor;

// Layouts
pres.defineSlideMaster({
  title: "TITLE",
  background: { color: C.background1 },
  objects: [
    { placeholder: { options: { name: "title", type: "title", x: 0.8, y: 1.6, w: 11.7, h: 1.2, fontSize: 44, bold: true, color: C.text2 }, text: "" } },
    { placeholder: { options: { name: "body", type: "body", x: 0.8, y: 2.8, w: 11.7, h: 0.7, fontSize: 22, color: C.text1 }, text: "" } },
  ],
});
pres.defineSlideMaster({
  title: "TITLE_ONLY",
  background: { color: C.background1 },
  margin: [0.5, 0.6, 0.6, 0.6],
  objects: [
    { line: { x: 0.6, y: 1.25, w: 12.1, h: 0, line: { color: HEX.accent3, width: 1 } } },
    { placeholder: { options: { name: "title", type: "title", x: 0.6, y: 0.35, w: 12.1, h: 0.8, fontSize: 30, bold: true, color: C.text2 }, text: "" } },
    { placeholder: { options: { name: "lead", type: "body", x: 0.6, y: 1.35, w: 12.1, h: 0.55, fontSize: 18, color: C.text1 }, text: "" } },
  ],
  slideNumber: { x: 12.4, y: 7.0, fontSize: 10, color: C.text1 },
});

function contentSlide(section, title, lead) {
  const s = pres.addSlide({ masterName: "TITLE_ONLY", sectionTitle: section });
  s.addText(title, { placeholder: "title" });
  if (lead) s.addText(lead, { placeholder: "lead" });
  return s;
}

function statCards(slide, items, y) {
  const w = 3.6, gap = 0.4, x0 = 0.6;
  items.forEach(([num, label], i) => {
    const x = x0 + i * (w + gap);
    slide.addShape(pres.shapes.ROUNDED_RECTANGLE, { x, y, w, h: 2.0, rectRadius: 0.08, fill: { color: C.background2 }, line: { color: C.background2 }, objectName: `card ${i + 1}` });
    slide.addText(num, { x: x + 0.25, y: y + 0.2, w: w - 0.5, h: 1.0, fontSize: 40, bold: true, color: C.accent1, margin: 0, isTextBox: true });
    slide.addText(label, { x: x + 0.25, y: y + 1.2, w: w - 0.5, h: 0.6, fontSize: 16, color: C.text1, margin: 0, isTextBox: true });
  });
}

function chartOpts(extra) {
  return Object.assign({
    x: 0.6, y: 2.0, w: 12.1, h: 4.9,
    showValue: true, dataLabelPosition: "outEnd", dataLabelFontSize: 11, dataLabelFontFace: "+mn-lt",
    catAxisLabelColor: HEX.dk1, valAxisLabelColor: HEX.dk1, catAxisLabelFontFace: "+mn-lt", valAxisLabelFontFace: "+mn-lt",
    valGridLine: { color: "E5E7EB", size: 0.5 }, catGridLine: { style: "none" },
    chartColors: [HEX.accent1], showLegend: false,
  }, extra);
}

function table(slide, rows, colW) {
  const head = rows[0].map((t) => ({ text: t, options: { bold: true, color: C.text2, fill: { color: C.background2 } } }));
  const body = rows.slice(1).map((r) => r.map((t) => ({ text: t })));
  slide.addTable([head, ...body], { x: 0.6, y: 2.1, w: 12.1, colW, fontSize: 16, color: C.text1, border: { type: "solid", pt: 0.5, color: "D1D9E6" }, rowH: 0.5 });
}

// 1 Title
pres.addSection({ title: "Overview" });
{
  const s = pres.addSlide({ masterName: "TITLE", sectionTitle: "Overview" });
  s.addText("Customer review 2026", { placeholder: "title" });
  s.addText("Northwind Retail, January–September", { placeholder: "body" });
  statCards(s, [["4.8 M€", "revenue"], ["1 240", "active customers"], ["92 %", "retention"]], 4.0);
}

// 2 Agenda
{
  const s = contentSlide("Overview", "Agenda");
  const items = ["Revenue by month", "Customers and segments", "Regions", "Churn and its causes", "The sales process", "Next steps"];
  s.addText(items.map((t, i) => ({ text: t, options: { bullet: { type: "number" }, breakLine: i < items.length - 1 } })),
    { x: 0.6, y: 1.6, w: 12.1, h: 4.5, fontSize: 22, color: C.text1, paraSpaceAfter: 10, isTextBox: true });
}

// 3 Revenue by month
pres.addSection({ title: "Revenue" });
const months = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep"];
{
  const s = contentSlide("Revenue", "Revenue by month", "Revenue grew every quarter; September was the best month.");
  s.addChart(pres.charts.BAR, [{ name: "Revenue k€", labels: months, values: [410, 432, 468, 495, 520, 548, 561, 590, 626] }],
    chartOpts({ barDir: "col", valAxisTitle: "k€", showValAxisTitle: true }));
}

// 4 Quarter over quarter
{
  const s = contentSlide("Revenue", "Quarter over quarter", "Each quarter added about a tenth.");
  table(s, [["Quarter", "Revenue k€", "Change"], ["Q1", "1 310", "–"], ["Q2", "1 563", "+19 %"], ["Q3", "1 777", "+14 %"]], [4, 4.1, 4]);
}

// 5 Segments
pres.addSection({ title: "Customers" });
{
  const s = contentSlide("Customers", "Customers by segment", "Retail chains bring half of the revenue.");
  s.addChart(pres.charts.DOUGHNUT, [{ name: "Share", labels: ["Retail chains", "Independent stores", "Online", "Wholesale"], values: [50, 28, 15, 7] }],
    { x: 0.6, y: 2.0, w: 12.1, h: 4.9, holeSize: 55, showLegend: true, legendPos: "r", legendFontFace: "+mn-lt", legendFontSize: 14,
      showPercent: true, showValue: false, dataLabelColor: "FFFFFF", dataLabelFontFace: "+mn-lt",
      chartColors: [HEX.accent1, HEX.accent2, HEX.accent3, HEX.accent4] });
}

// 6 Active customers
{
  const s = contentSlide("Customers", "Active customers", "Growth came from new online customers.");
  s.addChart(pres.charts.LINE, [{ name: "Customers", labels: months, values: [1050, 1072, 1101, 1124, 1150, 1171, 1188, 1215, 1240] }],
    chartOpts({ lineSize: 3, lineDataSymbol: "circle", lineDataSymbolSize: 7, dataLabelPosition: "t", valAxisMinVal: 1000, valAxisTitle: "customers", showValAxisTitle: true }));
}

// 7 Regions
{
  const s = contentSlide("Customers", "Regions", "The south grows fastest; the north is the largest.");
  table(s, [["Region", "Revenue k€", "Customers", "Growth"], ["North", "1 820", "410", "+6 %"], ["South", "1 390", "380", "+21 %"], ["East", "980", "260", "+11 %"], ["West", "610", "190", "+4 %"]], [3, 3.1, 3, 3]);
}

// 8 Churn
pres.addSection({ title: "Churn" });
{
  const s = contentSlide("Churn", "Churn", "Churn fell from 11 % to 8 % after the new onboarding.");
  statCards(s, [["8 %", "churn now"], ["11 %", "churn a year ago"], ["99", "customers lost"]], 2.4);
}

// 9 Why customers leave
{
  const s = contentSlide("Churn", "Why customers leave", "Price is the main reason, delivery times the second.");
  s.addChart(pres.charts.BAR, [{ name: "% of lost customers", labels: ["Other", "Service", "Product range", "Delivery time", "Price"], values: [7, 11, 17, 27, 38] }],
    chartOpts({ barDir: "bar", valAxisTitle: "% of lost customers", showValAxisTitle: true }));
}

// 10 Sales process
pres.addSection({ title: "Sales" });
{
  const s = contentSlide("Sales", "The sales process");
  const box = (t, x, y, shape) => {
    s.addShape(shape || pres.shapes.ROUNDED_RECTANGLE, { x, y, w: 1.8, h: 0.8, rectRadius: 0.08, fill: { color: C.accent1 }, line: { color: C.accent1 }, objectName: t });
    s.addText(t, { x, y, w: 1.8, h: 0.8, align: "center", valign: "middle", fontSize: 15, bold: true, color: C.background1, margin: 0, isTextBox: true });
  };
  const arrow = (x1, y1, x2, y2, label) => {
    s.addShape(pres.shapes.LINE, { x: x1, y: y1, w: x2 - x1, h: y2 - y1, flipV: y2 < y1, line: { color: HEX.dk1, width: 1.5, endArrowType: "triangle" } });
    if (label) s.addText(label, { x: (x1 + x2) / 2 - 0.4, y: Math.min(y1, y2) - 0.35, w: 0.8, h: 0.3, fontSize: 12, align: "center", color: C.text1, margin: 0, isTextBox: true });
  };
  box("Lead", 0.6, 3.2); box("Qualified", 2.9, 3.2); box("Offer", 5.2, 3.2);
  box("Accepted?", 7.5, 3.2, pres.shapes.DIAMOND);
  box("Customer", 10.6, 2.2); box("Follow-up", 10.6, 4.2);
  arrow(2.4, 3.6, 2.9, 3.6); arrow(4.7, 3.6, 5.2, 3.6); arrow(7.0, 3.6, 7.5, 3.6);
  arrow(9.3, 3.6, 10.6, 2.6, "yes"); arrow(9.3, 3.6, 10.6, 4.6, "no");
  s.addShape(pres.shapes.LINE, { x: 6.1, y: 5.0, w: 4.5, h: 0, line: { color: HEX.dk1, width: 1.5, dashType: "dash" } });
  s.addShape(pres.shapes.LINE, { x: 6.1, y: 4.0, w: 0, h: 1.0, flipV: true, line: { color: HEX.dk1, width: 1.5, dashType: "dash", endArrowType: "triangle" } });
}

// 11 SWOT
{
  const s = contentSlide("Sales", "Strengths and weaknesses");
  const q = [["Strengths", "wide range, fast restocking", C.accent1], ["Weaknesses", "delivery times in the north", C.accent2],
             ["Opportunities", "online customers, the south", C.accent4], ["Threats", "price competition", C.accent5]];
  q.forEach(([t, d, col], i) => {
    const x = 0.6 + (i % 2) * 6.15, y = 1.7 + Math.floor(i / 2) * 2.6;
    s.addShape(pres.shapes.RECTANGLE, { x, y, w: 5.95, h: 2.4, fill: { color: C.background2 }, line: { color: C.background2 }, objectName: t });
    s.addShape(pres.shapes.RECTANGLE, { x, y, w: 0.08, h: 2.4, fill: { color: col }, line: { color: col } });
    s.addText(t, { x: x + 0.35, y: y + 0.3, w: 5.4, h: 0.6, fontSize: 22, bold: true, color: col, margin: 0, isTextBox: true });
    s.addText(d, { x: x + 0.35, y: y + 1.0, w: 5.4, h: 0.9, fontSize: 18, color: C.text1, margin: 0, isTextBox: true });
  });
}

// 12 Two ways to grow
{
  const s = contentSlide("Sales", "Two ways to grow");
  [["Online", ["New customers every month", "Lower cost per order", "Needs faster delivery"]],
   ["The south", ["The fastest growing region", "Two new chains signed", "Needs a local warehouse"]]].forEach(([t, pts], i) => {
    const x = 0.6 + i * 6.15;
    s.addText(t, { x, y: 1.7, w: 5.95, h: 0.6, fontSize: 24, bold: true, color: C.text2, margin: 0, isTextBox: true });
    s.addText(pts.map((p, j) => ({ text: p, options: { bullet: true, breakLine: j < pts.length - 1 } })),
      { x, y: 2.4, w: 5.95, h: 3.0, fontSize: 20, color: C.text1, paraSpaceAfter: 8, isTextBox: true });
  });
}

// 13 Delivery times
pres.addSection({ title: "Plan" });
{
  const s = contentSlide("Plan", "Delivery times", "Deliveries in the north take twice as long as elsewhere.");
  s.addChart(pres.charts.BAR, [{ name: "Days", labels: ["North", "South", "East", "West"], values: [4.1, 2.2, 2.0, 2.4] }],
    chartOpts({ barDir: "col", valAxisTitle: "days", showValAxisTitle: true, dataLabelFormatCode: "0.0" }));
}

// 14 Plan for Q4
{
  const s = contentSlide("Plan", "Plan for Q4");
  s.addShape(pres.shapes.LINE, { x: 0.8, y: 2.6, w: 11.7, h: 0, line: { color: HEX.accent3, width: 3 } });
  [["Oct", "Warehouse", "Open the southern warehouse"], ["Nov", "Delivery", "Two-day delivery in the north"], ["Dec", "Pricing", "Volume prices for chains"]].forEach(([m, t, d], i) => {
    const cx = 2.3 + i * 4.2;
    s.addShape(pres.shapes.OVAL, { x: cx - 0.45, y: 2.15, w: 0.9, h: 0.9, fill: { color: C.accent1 }, line: { color: C.background1, width: 2 }, objectName: `badge ${m}` });
    s.addText(m, { x: cx - 0.45, y: 2.15, w: 0.9, h: 0.9, align: "center", valign: "middle", fontSize: 14, bold: true, color: C.background1, margin: 0, isTextBox: true });
    s.addShape(pres.shapes.ROUNDED_RECTANGLE, { x: cx - 1.8, y: 3.4, w: 3.6, h: 2.0, rectRadius: 0.08, fill: { color: C.background2 }, line: { color: C.background2 } });
    s.addText(t, { x: cx - 1.6, y: 3.6, w: 3.2, h: 0.5, fontSize: 20, bold: true, color: C.text2, margin: 0, isTextBox: true });
    s.addText(d, { x: cx - 1.6, y: 4.2, w: 3.2, h: 1.0, fontSize: 16, color: C.text1, margin: 0, isTextBox: true });
  });
}

// 15 Next steps
{
  const s = contentSlide("Plan", "Next steps");
  [["Southern warehouse", "Decision by 15 October"], ["Delivery partner", "Tender for the north"], ["Pricing", "Proposal to the board in November"]].forEach(([t, d], i) => {
    const x = 0.6 + i * 4.1;
    s.addShape(pres.shapes.ROUNDED_RECTANGLE, { x, y: 1.8, w: 3.9, h: 3.0, rectRadius: 0.08, fill: { color: C.background2 }, line: { color: C.background2 }, objectName: `step ${i + 1}` });
    s.addShape(pres.shapes.OVAL, { x: x + 0.3, y: 2.1, w: 0.7, h: 0.7, fill: { color: C.accent1 }, line: { color: C.accent1 } });
    s.addText(String(i + 1), { x: x + 0.3, y: 2.1, w: 0.7, h: 0.7, align: "center", valign: "middle", fontSize: 18, bold: true, color: C.background1, margin: 0, isTextBox: true });
    s.addText(t, { x: x + 0.3, y: 3.0, w: 3.3, h: 0.6, fontSize: 20, bold: true, color: C.text2, margin: 0, isTextBox: true });
    s.addText(d, { x: x + 0.3, y: 3.7, w: 3.3, h: 0.8, fontSize: 16, color: C.text1, margin: 0, isTextBox: true });
  });
}

(async () => {
  await pres.writeFile({ fileName: "deck.pptx" });
  await applyTheme("deck.pptx", THEME);
})();
