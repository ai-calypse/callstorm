# Callstorm evaluation dashboard

Interactive dashboard built with plain HTML, CSS, and JavaScript. No framework, dependencies, API keys, or build step required.

## Run locally

Extract this archive and open `callstorm-dashboard/index.html` in a modern browser.

Alternatively, from the extracted `callstorm-dashboard` directory run:

```sh
python -m http.server 8080
```

Then open http://localhost:8080.

## Files

- `index.html`: page structure, navigation, metric summary, and dialogs.
- `styles.css`: cream, olive, and pastel theme; responsive layouts.
- `app.js`: demo datasets, charts, tabs, load slider, run selection, baseline comparison, and evidence panels.

## Customize

Change the CSS variables at the top of `styles.css` to update the palette. The `runs`, `loads`, and `names` constants near the top of `app.js` define the sample data. Chart rendering and evidence panels use the selected run and tab from `state`.

All data, conversations, and traces are illustrative. No live voice agent or backend is connected. Optional WebMCP integration is feature-detected; it is not needed for ordinary dashboard use.

The archive contains the dashboard source from the deployed version. Hosting configuration and credentials are excluded.
