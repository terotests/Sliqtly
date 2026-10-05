// SPDX-License-Identifier: AGPL-3.0-or-later

/**
 * MCP tool definition for bitmap vectorizer.
 * Vectorizes PNG images to SVG using the Ranger tracer algorithm.
 */

export const vectorizerTool = {
  name: "vectorize_bitmap",
  description: "Convert a raster bitmap image (PNG) to a vector SVG using edge detection and curve fitting. Returns SVG paths.",
  inputSchema: {
    type: "object",
    properties: {
      image_data: {
        type: "string",
        description: "PNG image data as base64-encoded string",
      },
      color_count: {
        type: "number",
        description: "Number of color levels to quantize to (1-256, default: 4)",
        default: 4,
      },
      palette_mode: {
        type: "string",
        enum: ["quantize", "fixed"],
        description: "Color palette mode: 'quantize' from image, 'fixed' for pinned colors (default: quantize)",
        default: "quantize",
      },
      simplification: {
        type: "number",
        description: "Curve simplification level (0.0-10.0, lower=more complex paths, default: 0.25)",
        default: 0.25,
      },
      corner_threshold: {
        type: "number",
        description: "Corner detection threshold (0.0-1.0, default: 0.5)",
        default: 0.5,
      },
    },
    required: ["image_data"],
  },
};

/**
 * Tool handler - called by MCP server when vectorize_bitmap is invoked.
 * @param {Object} params - Tool parameters from MCP request
 * @param {string} params.image_data - Base64-encoded PNG data
 * @param {number} [params.color_count] - Color quantization levels
 * @param {string} [params.palette_mode] - Palette mode
 * @param {number} [params.simplification] - Curve simplification
 * @param {number} [params.corner_threshold] - Corner threshold
 * @returns {Promise<Object>} SVG result with paths and metadata
 */
export async function handleVectorizebitmap(params) {
  const { vectorizeImage, extractPaths, extractViewBox } = await import("../web/image-tracer.js");

  try {
    // Decode base64 image data
    const imageBuffer = Buffer.from(params.image_data, "base64");

    // Prepare tracer parameters
    const tracerParams = {
      colorCount: Math.min(256, Math.max(1, params.color_count || 4)),
      paletteMode: params.palette_mode || "quantize",
      opttolerance: params.simplification || 0.25,
      alphamax: params.corner_threshold || 0.5,
      lumaWeight: 3.0,
      snapRatio: 1.0,
      turdsize: 2,
    };

    // Vectorize the image
    const svg = await vectorizeImage(imageBuffer, tracerParams);

    // Extract paths and metadata
    const paths = extractPaths(svg);
    const viewBox = extractViewBox(svg);
    const pathCount = paths.length;

    return {
      success: true,
      svg: svg,
      paths: paths,
      viewBox: viewBox,
      pathCount: pathCount,
      message: `Successfully vectorized image into ${pathCount} paths`,
    };
  } catch (err) {
    return {
      success: false,
      error: err.message,
      message: `Vectorization failed: ${err.message}`,
    };
  }
}
