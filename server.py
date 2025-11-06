from asyncio import subprocess
import socket
import time
from http.server import BaseHTTPRequestHandler, HTTPServer
import os
import sys
import requests
import torch
import cv2
import numpy as np
import sqlite3
import asyncio
import threading
import websockets
import warnings
from onvif import ONVIFCamera
from zeep import Client


# pip install onvif-zeep
# https://www.youtube.com/watch?v=IasCZL072fQ

# Add yolov5 local path
sys.path.append('./yolov5')

global ws_client
ws_client = None

global cam_list
cam_list = []

# Load model
model = torch.hub.load('ultralytics/yolov5', 'yolov5s',pretrained=True)
device='cuda' if torch.cuda.is_available() else 'cpu'
model.eval()

class YOLORequestHandler(BaseHTTPRequestHandler):
    def _set_cors_headers(self):
        self.send_header('Access-Control-Allow-Origin', '*')
        self.send_header('Access-Control-Allow-Methods', 'POST, GET, OPTIONS')
        self.send_header('Access-Control-Allow-Headers', '*')

def detect_and_stream(ip):
    # backyard : 151
    # driveway : 244
    # front ln : 120
    global cam_list
    rtsp_url = "rtsp://admin:123456@"+ip+":554/Streaming/channels/101"
    client = set()
    object_list = {'person'} # for now just detecting person,'car','truck'}
    cap = cv2.VideoCapture(rtsp_url)
    if not cap.isOpened():
        print(f"Error: could not open rtsp stream for ip: ",ip)
        return
    
    start = time.time()
    while True:
        ret,frame = cap.read()
        if not ret:
            #continue
            threading.Thread(target=detect_and_stream,daemon=True,args=(ip,)).start()
            return
        frame = cv2.resize(frame,(960,960))
        results = model(frame)
        df = results.pandas().xyxy[0]
        df = df[(df['confidence'] > 0.3) & (df['name'].isin(object_list))]
        df["ip"] = ip
        json_data = df.to_json(orient='records')
        
        # print(json_data)
        asyncio.run(send_ai_data(json_data))
        end = time.time()
        elapsed = end - start
        if elapsed >= 7: # give each camera 7 seconds of video monitoring
            next_index = cam_list.index(ip)
            if next_index == (len(cam_list) - 1):
                next_index = 0
            else:
                next_index += 1
            next_ip = cam_list[next_index]
            threading.Thread(target=detect_and_stream,daemon=True,args=(next_ip,)).start()
            return

async def handlesocket(websocket):
        global ws_client
        ws_client = websocket # only ONE client is expected
        print(f"Client connected:{websocket.remote_address}")
        try:
            await ws_client.wait_closed()
        finally:
            print("client disconnected")

async def send_ai_data(data):
    global ws_client
    if ws_client != None:
        try:
            await ws_client.send(data)
            # print("sending data to client")
        except:
            return

async def detect_cam_sound(ip):
    RTSP_URL = "rtsp://admin:123456@"+ip+":554/Streaming/channels/101"
    ffmpeg_command = [
        'ffmpeg',
        '-i', RTSP_URL,
        '-acodec', 'pcm_s16le',  # Decode audio to PCM signed 16-bit little-endian
        '-ar', '44100',          # Set the audio sample rate (adjust as needed)
        '-ac', '1',              # number of channels
        '-f', 's16le',           # Set the output format to signed 16-bit little-endian
        '-' #'pipe:1'            # Output to standard output
    ]
    process = await asyncio.create_subprocess_exec(
        *ffmpeg_command,  # Use * to pass the list elements as separate arguments
        stdout=asyncio.subprocess.PIPE,
        stderr=asyncio.subprocess.DEVNULL
    )
    # process = subprocess.Popen(ffmpeg_command, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
    while True:
        try:
            if process.returncode is not None:
                print("wtf. process for cam ",ip," crashed????")
            # Read a chunk of audio data from the pipe
            raw_audio = await process.stdout.read(44100 * 2)  # Read 1 second of audio (sample_rate * 2 bytes/sample)
            
            if not raw_audio:
                print("breaking because no raw audio")
                break
            # Convert raw audio data to NumPy array
            audio_data = np.frombuffer(raw_audio, dtype=np.int16)
            
            
            # Perform audio analysis (e.g., sound level estimation)
            # You would use Librosa or other libraries here to process the audio_data
            
            # Simple example: Calculate the average amplitude
            average_amplitude = np.mean(np.abs(audio_data))
            
            # Set a threshold to detect sound
            sound_threshold = 500  # Adjust the threshold based on your camera and environment
            
            if average_amplitude > sound_threshold:
                # print("Sound threshold: ",average_amplitude,"@ cam: ",ip)
                sound_data = "[{\"cmd\":\"sound_data\",\"amplitude\":\""+str(average_amplitude)+"\",\"ip\":\""+ip+"\"}]"
                # print(sound_data)
                await send_ai_data(sound_data)
        except Exception as e:
            print("Error analyzing audio:", e)
            break
    print("Are we done here with cam ",ip, " ?")
    asyncio.create_task(detect_cam_sound(ip))

async def main():
    global cam_list
    warnings.filterwarnings("ignore",category=FutureWarning)
    os.system("clear")
    
    conn = sqlite3.connect("camview.db")
    cursor = conn.cursor()
    sql = "SELECT * FROM cameras;"
    cursor.execute(sql)
    #remember to not launcg "server.py" until AFTER all camera detection is complete
    rows = cursor.fetchall()
    cols = [description[0] for description in cursor.description]
    for row in rows:
        idx = 0
        for field in row:
            if(cols[idx]=="ip"):
                # print(cols[idx],"=>",field)
                cam_list.append(field)
            idx += 1

    sock = socket.socket(socket.AF_INET,socket.SOCK_STREAM)
    sock.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)
    sock.bind(("127.0.0.1",8765))
    sock.listen(128)
    sock.setblocking(False)
    async with websockets.serve(handlesocket,sock=sock,subprotocols=["Isabelle"]):
        
        # print("websocket server started")
        for ip in cam_list:
            asyncio.create_task(detect_cam_sound(ip))

        # i'm commenting out object detection because we don't offer
        # object detection on the first release, just sound detection
        # only launch video monitor thread for fisrt cam, others will alternate
        threading.Thread(target=detect_and_stream,daemon=True,args=(cam_list[0],)).start()

        
        await asyncio.Future()
if __name__ == '__main__':
    asyncio.run(main())