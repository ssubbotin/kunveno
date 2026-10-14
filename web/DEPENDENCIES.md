# Pinned dependencies

## @mediapipe/tasks-vision: exactly 0.10.35

Person segmentation for virtual backgrounds uses `@mediapipe/tasks-vision`, pinned with
`--save-exact` to 0.10.35. The 1.x releases report usage to `odml.pa.googleapis.com` from inside the
library; kunveno runs on private networks and makes no requests outside its own server. 0.10.35 has
no such call. The WebAssembly loader, the `.wasm` binary and the model all ship in the bundle
(`src/lib/background/segmenter.ts`, `src/lib/background/model/`), so nothing is loaded from a CDN.

Before upgrading, install the candidate version and check the bundle:

```bash
grep -c odml.pa.googleapis node_modules/@mediapipe/tasks-vision/vision_bundle.mjs   # must print 0
grep -o 'https\?://[a-z0-9.-]*' node_modules/@mediapipe/tasks-vision/vision_bundle.mjs \
  node_modules/@mediapipe/tasks-vision/wasm/vision_wasm_module_internal.js | sort -u  # review every host
```

Then run the segmenter on a production build (`npm run build`, `npx vite preview`) and confirm in
the browser's network list that every request goes to the serving host. In 0.10.35 the only
`googleapis` string in `vision_bundle.mjs` is a protobuf type name
(`type.googleapis.com/mediapipe.tasks.vision.holistic_landmarker...`), which is never requested.
