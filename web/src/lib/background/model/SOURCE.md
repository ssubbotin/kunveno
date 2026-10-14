# selfie_segmenter_landscape.tflite

- Source: https://storage.googleapis.com/mediapipe-models/image_segmenter/selfie_segmenter_landscape/float16/latest/selfie_segmenter_landscape.tflite
- Downloaded: 2026-10-14
- Size: 250177 bytes
- SHA-256: `490e9ea734313e0de10fa0cd9e3c6133e36ea4db2b7a49bde9ef019f72796b8e`
- License: Apache-2.0 (MediaPipe Selfie Segmentation model card)
- Input 256x144, output one confidence mask of the same size.

The file is committed so the client never fetches it from Google: the build emits it as a hashed
asset next to the rest of the bundle, and the segmenter loads it from there.
