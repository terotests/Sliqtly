---
title: Formulas on slides
transition: fade
seconds: 0.6
step: 1.2
hold: 2.5
---

# Formulas on slides

Maths right in Markdown: `$…$` inside a line, `$$…$$` on a line of its own.
{.lead}

::: notes
Formulas are written in LaTeX notation, and they are drawn in the same shapes
in the presentation, the PDF and PowerPoint.
:::

## Pythagoras

In a right triangle, the legs $a$ and $b$ and the hypotenuse $c$ satisfy

$$a^2 + b^2 = c^2$$

For example $3^2 + 4^2 = 25 = 5^2$.

## The quadratic equation

The solutions of $ax^2 + bx + c = 0$ are

$$x = \frac{-b \pm \sqrt{b^2 - 4ac}}{2a}$$

The discriminant $D = b^2 - 4ac$ tells how many real solutions there are.

## Derivative and integral

$$f'(x) = \lim_{h \to 0} \frac{f(x+h) - f(x)}{h}$$

$$\int_0^\infty e^{-x^2}\,dx = \frac{\sqrt{\pi}}{2}$$

## Sums

$$\sum_{i=1}^{n} i = \frac{n(n+1)}{2} \qquad \sum_{i=1}^{n} i^2 = \frac{n(n+1)(2n+1)}{6}$$

$$\sum_{k=0}^{\infty} \frac{1}{k!} = e \approx 2.718$$

## Matrices and piecewise functions

$$\begin{pmatrix} a & b \\ c & d \end{pmatrix}^{-1} = \frac{1}{ad - bc} \begin{pmatrix} d & -b \\ -c & a \end{pmatrix}$$

$$|x| = \begin{cases} x & \text{if } x \ge 0 \\ -x & \text{otherwise} \end{cases}$$
