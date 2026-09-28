---
title: Kaavat kalvoilla
transition: fade
seconds: 0.6
step: 1.2
hold: 2.5
---

# Kaavat kalvoilla

Matematiikkaa suoraan Markdownissa: `$…$` rivin sisällä, `$$…$$` omalla rivillään.
{.lead}

::: notes
Kaavat kirjoitetaan LaTeX-merkinnällä, ja ne piirretään samoina muotoina
esitykseen, PDF:ään ja PowerPointiin.
:::

## Pythagoras

Suorakulmaisessa kolmiossa kateetit $a$ ja $b$ sekä hypotenuusa $c$ toteuttavat

$$a^2 + b^2 = c^2$$

Esimerkiksi $3^2 + 4^2 = 25 = 5^2$.

## Toisen asteen yhtälö

Yhtälön $ax^2 + bx + c = 0$ ratkaisut ovat

$$x = \frac{-b \pm \sqrt{b^2 - 4ac}}{2a}$$

Diskriminantti $D = b^2 - 4ac$ kertoo, montako reaalista ratkaisua on.

## Derivaatta ja integraali

$$f'(x) = \lim_{h \to 0} \frac{f(x+h) - f(x)}{h}$$

$$\int_0^\infty e^{-x^2}\,dx = \frac{\sqrt{\pi}}{2}$$

## Summat

$$\sum_{i=1}^{n} i = \frac{n(n+1)}{2} \qquad \sum_{i=1}^{n} i^2 = \frac{n(n+1)(2n+1)}{6}$$

$$\sum_{k=0}^{\infty} \frac{1}{k!} = e \approx 2.718$$

## Matriisit ja paloittain määritellyt funktiot

$$\begin{pmatrix} a & b \\ c & d \end{pmatrix}^{-1} = \frac{1}{ad - bc} \begin{pmatrix} d & -b \\ -c & a \end{pmatrix}$$

$$|x| = \begin{cases} x & \text{kun } x \ge 0 \\ -x & \text{muuten} \end{cases}$$
