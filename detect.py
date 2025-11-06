from fastapi import FastAPI,Request
import torch
from io import BytesIO
from PIL import Image
import uvicorn
import os

os.environ["TORCH_LOGS"] = "none"
os.environ["PYTORCH_JIT_LOG_LEVEL"] = "0"
os.environ["TORCH_CPP_LOG_LEVEL"] = "ERROR"

app = FastAPI()
model = torch.hub.load('ultralytics/yolov5','yolov5s',pretrained=True)

@app.post("/detect")
async def detectObj(req: Request):
    data = await req.body()
    img = Image.open(BytesIO(data)).convert("RGB")
    results = model(img)

    detections = []
    for *box, conf, cls in results.xyxy[0].tolist():
        detections.append({
            "class": model.names[int(cls)],
            "confidence": float(conf),
            "box": [float(x) for x in box]
        })
    return {"detections": detections}

if __name__ == "__main__":
    uvicorn.run(app,host="0.0.0.0", port=8000)

"""
'pip install --no-cache-dir "gitpython>=3.1.30" "pillow>=10.3.0" "requests>=2.32.2" "setuptools>=70.0.0" "urllib3>=2.5.0 ; python_version > "3.8"" ' returned non-zero exit status 2.

"""