# Bitmap Tracer Integration Plan

This document outlines the implementation of image vectorization in Sliqtly.

## Infrastructure (COMPLETED)

### 1. Node.js Wrapper (`web/image-tracer.js`)
- Wraps `lib/evg/tools/evg_trace_cli.rgr` (Ranger compiler output)
- `vectorizeImage(imageData, params)` - converts PNG → SVG
- `extractPaths(svg)` - extracts path objects from traced SVG
- `extractViewBox(svg)` - extracts SVG dimensions
- Parameters: colorCount, alphamax, opttolerance, lumaWeight, snapRatio, etc.

### 2. Build Script (`scripts/compile-tracer.mjs`)
Run before using the tracer:
```bash
npm run compile:tracer
```
This compiles `ranger/lib/evg/tools/evg_trace_cli.rgr` to `lib/evg/bin/evg_trace_cli.js`

### 3. MCP Tool Definition (`mcp-go/tools-vectorizer.mjs`)
Exposes `vectorize_bitmap` tool:
- Input: PNG as base64 string
- Output: SVG with paths, viewBox, metadata
- Claude clients can call this to vectorize images

---

## Next Steps (TO DO)

### Phase 1: MCP Integration (1 day)

**File: `mcp-go/rgr/Tools.rgr`**
- Add `vectorize_bitmap` method that wraps `tools-vectorizer.mjs`
- Integrate with McpHost infrastructure
- Schema: base64 PNG → SVG paths + metadata

**Testing:**
```bash
npm run check   # Verify Ranger compilation
curl -X POST http://localhost:8080/mcp \
  -H "Content-Type: application/json" \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "tools/call",
    "params": {
      "name": "vectorize_bitmap",
      "arguments": {
        "image_data": "<base64 PNG>",
        "color_count": 4
      }
    }
  }'
```

### Phase 2: UI Component (2-3 days)

**File: `web/main.js`**
- Add `vectorizeImage(path)` function (similar to `openImageEditor`)
- Call `/vectorize_bitmap` MCP tool on traced image
- Replace PNG reference with SVG in document

**Example flow:**
```javascript
// After image edit completes
async function vectorizeAfterEdit() {
  const blob = adjusting.blob;  // Original PNG
  const svg = await callMcp("vectorize_bitmap", {
    image_data: btoa(blob),  // Base64 encode
    color_count: 4
  });
  // Replace picture reference in document
  const svgFile = await keepPicture(svg, "image/svg+xml", path);
  app.updateImage(path, svgFile);
}
```

**File: Update PresChartEditor (Ranger)**
- Add "Vectorize" button alongside "Save" in adjust mode
- Call vectorizeAfterEdit() when clicked
- Show progress during vectorization

### Phase 3: Document Settings (1 day)

**File: `mcp-go/rgr/Tools.rgr` (or Deck.rgr)**
- Add `backgroundImage` field to presentation metadata
- In Document Settings modal, add "Background image" input
- CSS: apply as `background-image: url(...)`

**Schema:**
```json
{
  "presentation": {
    "title": "My Presentation",
    "backgroundImage": "/images/background.svg",
    "transition": "fade"
  }
}
```

### Phase 4: Background Image Editor (1-2 days)

**Flow:**
1. Click "Edit" on background image in Document Settings
2. Opens same image editor as regular images (crop, adjust)
3. Add "Vectorize" option alongside Save
4. On save/vectorize, update presentation's backgroundImage field
5. Preview updates in real-time

**Implementation:**
- Reuse `openImageEditor()` pattern
- Add vectorize callback on save
- Update presentation metadata when done

---

## Architecture Diagram

```
User clicks "Vectorize" on image
            ↓
   PresChartEditor (Ranger)
            ↓
      main.js web handler
            ↓
   vectorizeImage() [PNG blob]
            ↓
   /vectorize_bitmap MCP call
            ↓
   mcp-go/tools-vectorizer.mjs
            ↓
   web/image-tracer.js
            ↓
   spawn: node lib/evg/bin/evg_trace_cli.js
            ↓
   evg_trace_cli (compiled Ranger tracer)
            ↓
   SVG output → extract paths → return to Claude
            ↓
   Replace image ref in document
            ↓
   Slides update with vector graphic
```

---

## API Reference

### `vectorizeImage(imageData, params)`
**Parameters:**
- `imageData` {Buffer|Uint8Array} - PNG bytes
- `params` {Object}:
  - `colorCount` {number} - 1-256 (default: 4)
  - `paletteMode` {string} - "quantize" | "fixed" (default: "quantize")
  - `lumaWeight` {number} - color weight (default: 3.0)
  - `alphamax` {number} - corner threshold (default: 0.5)
  - `opttolerance` {number} - curve fit tolerance (default: 0.25)
  - `turdsize` {number} - small speck removal (default: 2)

**Returns:** `Promise<string>` - SVG content

### `extractPaths(svg)`
**Returns:** `Array<{fill, d}>`
- `fill` - hex color ("#RRGGBB")
- `d` - SVG path data

### `extractViewBox(svg)`
**Returns:** `string` - "x y width height"

---

## Testing

### Unit Test
```bash
# Test tracer compilation
npm run compile:tracer

# Test image-tracer module
node -e "
  import('./web/image-tracer.js').then(async m => {
    const fs = require('fs');
    const png = fs.readFileSync('test-image.png');
    const svg = await m.vectorizeImage(png, {colorCount: 4});
    console.log('Traced paths:', m.extractPaths(svg).length);
  });
"
```

### Integration Test
- Save presentation with background image
- Click "Edit" on background
- Select "Vectorize"
- Verify SVG output matches PNG visually
- Check file size reduction (PNG → SVG)

---

## Performance Considerations

- Tracer timeout: 30 seconds (configurable in image-tracer.js)
- Max buffer: 10MB output (adjust for large images)
- Temp files cleaned up after each trace
- Caching: store traced results with PNG hash to avoid re-tracing

**Optimization opportunities:**
1. Worker thread for tracer subprocess
2. Incremental tracing for large images
3. Server-side caching of traces

---

## Known Issues / TODOs

- [ ] Ranger compilation step required before tracer works
- [ ] Error handling for failed traces (fallback to original)
- [ ] UI feedback during long-running traces
- [ ] Palette customization UI (currently fixed defaults)
- [ ] Support for multi-layer output (currently flattened)
- [ ] Document Settings "Background image" not yet in schema
- [ ] Sliqtly MCP server integration not yet wired up

---

## References

- Ranger tracer: `terotests/ranger/lib/evg/tools/evg_trace_cli.rgr`
- Ranger docs: https://terotests.github.io/Ranger/docs/
- Logo vectorization example: `ranger/landing/tools/logo.mjs`
- Current image editor: `web/image-adjust.js`, `web/main.js` (line ~300)
