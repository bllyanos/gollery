# Gollery visual system

## Admin workflow update

The admin dashboard uses a compact 1.6–2rem title and 24px vertical heading spacing instead of a display-size hero, leaving photo management prominent. Its photo management area is a responsive list: one row per photo, with a small thumbnail, editable title, comma-separated reusable tags, explicit visibility status/action, individual delete, and a selection checkbox. The selection bar offers a confirmed delete for up to 50 photos. Its separate bulk-upload page shares the mint panel, coral primary button, visible return link, and mint success notice. The upload form lists the 10-image, 15 MiB-per-image, 60 MiB aggregate-image, and 61 MiB multipart-request limits. Batch upload stays separate from the single-image dashboard form.

## Direction

A straightforward family photo gallery with soft-brutalist structure: photographs near the top, crisp ink edges, warm paper, and a few tactile pastel surfaces. The admin page is a working desk, with upload beside a clearly titleed set of photo controls. No decorative control implies an unavailable feature.

## Palette and contrast

The canonical CSS variables live in `static/site.css`: canvas `#f7f3e9`, paper `#fffdf7`, ink `#252923`, muted text `#555b52`, separator `#aeb4a7`, coral `#f4a48d`, pale coral `#f9ded2`, mint `#d6e9dc`, lilac `#e2ddf1`, butter `#f5e6ac`, danger text/border `#8b3136`. Use ink for text on every pastel; never use the pastel as text. Ink on paper, canvas and accents and muted text on paper exceed WCAG AA 4.5:1. Ink outlines and focus indicators exceed 3:1 against adjacent surfaces. Destructive actions combine the text “Delete photo” with a dark red border/text, and hidden status is explicitly titleed.

## Typography and geometry

Use local system UI sans, 16px body with 1.5 line-height; compact uppercase tracked eyebrows and admin section metadata; 800-weight fluid display titles for access pages, and compact 1.6–2rem titles for the family gallery and admin pages. The family photo count uses sentence case and quieter weight so it reads as metadata, not another title. A 4px spacing rhythm produces 8/12/16/24/32px gaps. Controls have 9px radii, cards 16–18px; key boundaries use 2px ink borders, minor dividers 1px muted lines. Only cards and primary controls get hard 3–5px offset ink shadows, never blurry elevation or gradients.

The shared shell caps at 1320px with fluid side gutters. Gallery cards form three columns on wide screens, two on medium, and one on mobile. Their large, 6:5 image windows (5:4 on mobile) use a narrow pastel mat and `object-fit: cover`; only previews are cropped, while opening one displays the full original in a native dialog. Admin previews use compact square crops beside always-visible status and controls. Admin uses a 350px upload column beside a single-column photo list on wide screens; it stacks the upload above the collection on smaller screens. At 320px, gutters, flexible actions and full-width fields avoid horizontal overflow.

## Components and behavior

The header wordmark uses a CSS-only geometric mark and names the product, while the authenticated family's “Shared photos” h1 names the page. Sign-out remains in the compact header and the quieter photo count sits beside the page title directly above the grid, with no hero or introductory copy; the admin page uses a compact task heading and direct link to bulk upload. Gallery card mats rotate through muted pastels, with titles in a separate ink-divided footer. Tag chips use outlined pastel pills inspired by GitHub labels; native All/tag buttons above the grid filter the already-rendered cards without network requests, with pressed state and an empty-result announcement. Upload has a mint heading and primary coral action. Admin rows group selection, thumbnail, visibility status, title and tag editing, and actions; delete remains visually distinct. Access forms reuse the same bordered card and fields, with lilac family and mint admin banners.

Buttons lift by 2px on hover, press down on active, and have a visible 3px ink `:focus-visible` outline. Gallery photos gently zoom on hover within their fixed frame and keep a visible keyboard focus outline; admin previews do not imply a click action. The photo dialog uses native focus containment and Escape dismissal, a titleed close action, and a protected single-photo download link. Each card has a checkbox with an explicit Select title; the small selection bar announces the count and downloads up to 50 photos as a ZIP. Inputs and links retain native keyboard operation, titles, validation and focus. Motion is disabled for reduced-motion users. No icons or font dependencies; the only arrow is textual navigation back to the gallery. Without JavaScript, gallery links still open the protected image directly and the selection form still works. All media and archive authorization is enforced by the server.
