# Callstorm review studio

A separate design exploration. The original dashboard files are unchanged.

```sh
go run ./cmd/dashboard -runs runs -addr 127.0.0.1:8182
# http://127.0.0.1:8182/studio/
```

The studio uses the existing report API, measurements, estimates, and scoring.
The overview combines a compact result summary, an interactive median/p95
chart, prioritized review notes, and the load-results grid. Dedicated views
hold caller experience, task results, comparisons, findings, and evidence.
A run that is part of a suite also gets **Compare scenarios**: every part on
one axis for reply wait, task success with 95% ranges, the slowest reply
position, wait bands, mishearing against outcomes pooled across the suite, and
the remaining conversation figures side by side.
Metric cards lead to the corresponding evidence; definitions remain in the
sidebar. Formulas expand within evidence. The link in the sidebar opens the
original dashboard.

`index.html` is an independent copy. `studio.css` layers the new presentation
over the parent's existing styles. No new runtime packages.

```sh
node cmd/dashboard/ui/studio/render_check.mjs
```
