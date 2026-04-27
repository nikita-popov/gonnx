"""ResNet-50 image classification handler for gonnx.

Expects an ONNX model exported from torchvision resnet50 with
input shape [1, 3, 224, 224] and ImageNet-1k labels in assets/labels.txt.
"""
from __future__ import annotations

import base64
import io

import numpy as np
import onnxruntime as ort
from PIL import Image

from gonnx import ModelWorker, Request, Response, WorkerContext


class ResNet50Worker(ModelWorker):
    def load(self, ctx: WorkerContext) -> None:
        self.sess = ort.InferenceSession(
            ctx.model_path,
            providers=ctx.providers,
        )
        with open(ctx.asset("assets/labels.txt")) as f:
            self.labels = [line.strip() for line in f]

    def predict(self, req: Request) -> dict:
        image_bytes = base64.b64decode(req.json["image"])
        top_k = req.json.get("top_k", 5)

        image = Image.open(io.BytesIO(image_bytes)).convert("RGB")
        tensor = self._preprocess(image)

        (logits,) = self.sess.run(None, {"input": tensor})
        probs = self._softmax(logits[0])

        top_idx = np.argsort(probs)[::-1][:top_k]
        return {
            "classes": [
                {"label": self.labels[i], "score": float(probs[i])}
                for i in top_idx
            ]
        }

    @staticmethod
    def _preprocess(image: Image.Image) -> np.ndarray:
        image = image.resize((224, 224))
        arr = np.array(image, dtype=np.float32) / 255.0
        mean = np.array([0.485, 0.456, 0.406])
        std = np.array([0.229, 0.224, 0.225])
        arr = (arr - mean) / std
        return arr.transpose(2, 0, 1)[np.newaxis]  # NCHW

    @staticmethod
    def _softmax(x: np.ndarray) -> np.ndarray:
        e = np.exp(x - x.max())
        return e / e.sum()


app = ResNet50Worker()
