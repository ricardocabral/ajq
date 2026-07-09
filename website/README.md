# ajq website

The public website and documentation for **ajq**, built with
[Hugo](https://gohugo.io/) and the [Docsy](https://www.docsy.dev/) theme, and deployed to
GitHub Pages by [`.github/workflows/deploy-website.yml`](../.github/workflows/deploy-website.yml).

## Structure

```
website/
├── hugo.toml                 # site config (Docsy params, menus, modules)
├── go.mod / go.sum           # Hugo module deps (Docsy → Bootstrap, Font-Awesome)
├── package.json              # PostCSS/autoprefixer for the Docsy asset pipeline
├── assets/scss/              # brand overrides (_variables_project, _styles_project)
├── assets/icons/logo.svg     # navbar wordmark
├── static/favicon.svg        # favicon glyph
├── static/images/brand/      # generated logo + banner (PNG); banner is the OG image
└── content/en/
    ├── _index.md             # landing page (concise: pitch, comparison, benchmarks)
    └── docs/                 # documentation
        ├── tutorials/        #   walkthroughs
        ├── how-to/           #   task recipes
        ├── reference/        #   CLI and behavior reference
        └── explanation/      #   design notes
```

Documentation is split into walkthroughs, task recipes, reference pages, and design
notes.

## Prerequisites

- [Hugo Extended](https://gohugo.io/installation/) ≥ 0.146
- [Go](https://go.dev/dl/) (Hugo modules resolve Docsy and its dependencies)
- [Node.js](https://nodejs.org/) (PostCSS / autoprefixer for the SCSS pipeline)

## Local development

```bash
cd website
npm install          # PostCSS + autoprefixer
hugo mod get ./...   # fetch Docsy, Bootstrap, Font-Awesome (first run only)
hugo server          # http://localhost:1313/
```

Build the production site into `public/`:

```bash
hugo --gc --minify
```

## Deployment

Every push to `main` that touches `website/**` triggers the **Deploy website** workflow,
which builds the site and publishes it to GitHub Pages. Pull requests build the site as a
check but do not deploy.

**One-time repo setting:** in **Settings → Pages**, set *Build and deployment → Source* to
**GitHub Actions**. No `gh-pages` branch is used.

## Notes

- Benchmark figures on the landing page are representative measurements on the default
  local backend; they're labelled with the hardware/model caveat since real numbers vary
  by machine, model, and data.
- The palette, typography (Space Grotesk + JetBrains Mono), and brand marks are
  reconciled with the **ajq Design System** (the "icy/glacial tech" Claude Design
  project): Ice cyan signal, Aurora periwinkle accent, cool Slate neutrals.
- Logo and banner in `static/images/brand/` were generated to match that system; the
  banner (`ajq-banner.png`, 1280×640) is wired as the Open Graph / Twitter preview image.
