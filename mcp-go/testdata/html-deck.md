---
title: HTML test
---
# HTML test

Tags the guide lists, tags it does not, tables and safety.
{.lead}

## 3 · span styles

- <span style="background-color:#ffd54a;color:#111">background</span>
- <span style="font-size:32pt">big</span> and <span style="font-size:12pt">small</span>
- <span style="font-weight:bold;font-style:italic">bold italic</span>
- <span style="letter-spacing:2px">spaced</span>

## 4 · Block HTML

<div style="background:#1d2a6b;padding:20px;border-radius:12px">
div with a background and padding
</div>

## 5 · HTML lists

<ul>
<li>ul list</li>
<li>second <b>bold</b></li>
</ul>

<ol start="3">
<li>ol start=3</li>
<li>fourth</li>
</ol>

## 7 · Table: caption, list and link in a cell, nested

<table>
<caption>Table caption</caption>
<tr><th>Cell</th><th>Content</th></tr>
<tr><td>List</td><td><ul><li>one</li><li>two</li></ul></td></tr>
<tr><td>Nested</td><td><table><tr><td>a</td><td>b</td></tr></table></td></tr>
<tr><td>Formula</td><td style="background:#3fb68b">$E=mc^2$</td></tr>
</table>

## 10 · Embeds

<iframe src="https://example.com" width="600" height="300"></iframe>

<video src="https://example.com/v.mp4" controls></video>

<svg width="200" height="100"><circle cx="50" cy="50" r="40" fill="#5ce1ff"/></svg>

## 11 · Safety

<script>alert('xss')</script>

<a href="javascript:alert(1)">javascript link</a>

<span onclick="alert(1)" style="color:#ffd54a">onclick span</span>
