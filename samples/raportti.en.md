---
title: Quarterly report Q4
transition: fade
header-left: "{title}"
header-right: Example Ltd
footer-left: Confidential
footer-right: "{page} / {pages}"
header-skip: first last
footer-skip: first last
---

# Quarterly report Q4

Header, footer, page numbers and their style
{.lead}

::: notes
The cover and the last slide have no header or footer (header-skip, footer-skip).
:::

## Revenue

Revenue grew **7 %** to €18.7M.

1. Consulting +7 %
2. Services +7 %
3. Products +4 %
{.build}

## How the header and footer are written

In the front matter each edge has three places:
`header-left`, `header-center`, `header-right`, and the same starting with `footer-`.

- `{page}` and `{pages}` are the slide number and the slide count, `{title}` the presentation's title
- A logo: `header-right: ![](media/logo.png)`
- `footer-skip: first last` leaves the cover and the end without a footer
- In the theme: `footer { background-color: #1e1b4b; color: #fff; font-weight: bold }`

## A section slide without them {furniture=off}

`{furniture=off}` on the heading, or just `{header=off}` / `{footer=off}`.

## Thank you

Questions?
