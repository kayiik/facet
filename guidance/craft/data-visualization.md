# Data visualization

Video is not a spreadsheet. The viewer cannot pause, scroll or zoom, and each chart gets five to eight seconds. Show one finding per chart, build it in motion, hold it long enough to read, and keep every number true to its source.

## Pick the form

Stop at the first match:

1. **No data:** a `text_card`.
2. **One number to land:** `stat_card`, full screen with an impact entrance. A compact number beside other content: `stat_reveal`.
3. **Two values, before and after, A versus B:** `comparison`.
4. **Progress toward a goal, a share of 100:** `progress_bar`.
5. **Three to six headline metrics:** `kpi_grid`.
6. **Quantities across categories, or a ranking:** `bar_chart`, sorted largest first for rankings.
7. **A trend over time:** `line_chart`. Two related series on one chart instead of a scatter plot, which is too dense for video.
8. **Parts of a whole:** `pie_chart`, as a donut with the total in the centre.
9. **Anything else:** `bar_chart`; it is the most readable form.

Fewer than three data points never make a chart; say it as a stat ("Revenue grew 40% to $2.1M"). More than twelve, aggregate first. A qualitative comparison is a side-by-side of images or words, not a chart. Data that needs 30 seconds to read is several charts across several scenes.

Fields for each scene type are in `guidance/runtimes/scene-types.md`.

## How much fits

| Form | Ideal | Most |
|---|---|---|
| `bar_chart` | 5–7 bars | 9 |
| `line_chart` | 5–12 points | 15, labelled sparsely |
| `pie_chart` | 3–5 slices | 6 |
| `kpi_grid` | 3–6 metrics | 6 |

To simplify: show the top five to seven and fold the rest into "Other"; aggregate time (daily to weekly, monthly to quarterly); split several metrics across scenes; show percentages when values span orders of magnitude; round hard ($1,234,567 becomes $1.2M). Vertical frames are narrower: use fewer bars, not smaller ones.

## Build it in motion

Every chart animates; a static chart is a slide.

- **Build-up (default):** the empty frame with title and axes, then the data: bars grow from the baseline left to right with a short stagger, lines draw left to right, slices fill clockwise from twelve o'clock largest first, numbers count up from zero.
- **Narrative highlight:** when the narrator walks through points, one element at a time comes to full colour while the rest drop to about 30% opacity.
- **Comparison reveal:** show the baseline, hold it, then animate to the new values and bring in the change labels (+40%, −15%).

| Moment | Animation | Hold |
|---|---|---|
| Chart build | 2–4 s | 3–5 s |
| Highlight one element | 0.3–0.5 s | 2–3 s |
| Comparison change | 1–2 s | 3–5 s |
| Counter in a `kpi_grid` or `stat_card` | 1.5–2 s | 2–3 s |
| A label or annotation | 0.2–0.3 s | stays |

The chart is fully built and held at least 3 s before the scene changes, so a chart scene runs at least 5 s and usually 5 to 8. If the narration moves on before the chart can be read, extend the scene or simplify the chart.

Templated chart scenes animate their own build. To follow a narration point by point, split the data story into consecutive scenes tied to the lines that name each point (`lines` in `edit_decisions`), for example `kpi_grid` for the overview, `bar_chart` for the breakdown, `line_chart` for the trend. Highlight sequences, morphs and custom counters are atelier work: `guidance/vendor/remotion-best-practices/rules/charts.md` and `guidance/vendor/hyperframes-animation/blueprints/dataviz-countup.md`.

## Say the finding

- The narration states the one takeaway and the number that proves it; it does not read every value.
- The title states the finding where it can ("Vector search answers in 12 ms"), or names the measure and unit clearly ("Query time, ms").
- Bring the visual in when the line that needs it is spoken, not before.

## Labels

- **Bars:** values above each bar (inside if the bar is tall enough); category names below, one or two words. Long names mean fewer bars or a different form.
- **Lines:** values at the start and the end and at notable peaks or valleys, never on every point. Multi-series: name each line beside it rather than in a legend.
- **Pies:** slices of 10% or more labelled inside with the percentage; smaller ones outside with a leader line; combine anything under 5% into "Other"; always percentages, not only raw values.
- **KPI grids:** the big number centred, the label below it, a small arrow and percentage for the change; a 2×2 or 3×2 grid with equal cards.
- **Every chart:** a title, units in the title or on the axis, a small "Source: …" line for external data, and nothing unlabelled that the narration does not explain.

Minimum sizes at 1920×1080: title 32 px (36 to 40 better), axis and value labels 24 px (28 better), annotations 20 px, source line 16 px. Double them at 4K; never go below them at 720p. Other text follows `guidance/craft/typography.md`.

## Colour

- Take chart colours from the style: the primary colour for the main series, the accent for the one that matters, lighter or darker steps of the primary for the rest; text, background and gridlines from the style too.
- The key data point gets the accent at full saturation, perhaps a slight scale-up; context data is muted; baselines and targets are dashed in the muted colour.
- At most four or five colours in one chart.
- Never colour alone: every bar, slice and line carries a text label. Avoid red against green as the only difference; blue against orange or yellow reads for everyone. Adjacent elements differ by at least 3:1 in contrast.
- Gridlines are light, or absent.

## Keep it honest

| Pitfall | Fix |
|---|---|
| Bar axis not starting at zero | Bars always start at zero |
| 3D charts | Flat 2D only; perspective distorts size |
| Two y-axes with different scales | Two charts side by side |
| A cherry-picked time range | Show the relevant range, or say it is cut |
| Too many pie slices | Five or six at most |

- Every number traces to the `research_brief` with its source, and is rounded consistently.
- Charts and numbers are rendered by the composition as data and text, never drawn by an image or video generator, which invents digits.

## In the scene plan

For each chart scene, write the form, the data with units and source, the build and hold ("bars grow left to right over 2.5 s with a 0.3 s stagger, hold 5 s"), which element is highlighted and in which colour, the title and source line in the overlay notes, and the narration lines it covers. Example:

```text
scene s7, bar_chart, lines l14–l15, 8 s
data: traditional database 450 ms, vector database 12 ms, cached 3 ms (sourced in the research_brief)
build: bars grow over 2.5 s, 0.3 s stagger; hold 5 s; vector database bar in the accent, others muted
overlays: title "Query time, ms"; "Source: …" bottom right
```

## Quality checks

- The form matches the data story; the data fits the limits above.
- Build 2 to 4 s and hold at least 3 s; every frame of the hold is readable when paused.
- Titles, labels and units present and at the minimum sizes; the source shown for external data.
- Colours from the style; the key point emphasised; no meaning carried by colour alone.
- Bars start at zero; no 3D; no dual axes.
- Every number matches the `research_brief`; none sits inside a generated image.
