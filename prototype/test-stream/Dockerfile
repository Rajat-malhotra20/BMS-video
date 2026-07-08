FROM jrottenberg/ffmpeg:4.4-alpine
COPY 142755-780943401_medium.mp4 /video.mp4
ENTRYPOINT ["sh", "-c"]
COPY fixed-fleet.json /fixed-fleet.json
CMD ["while true; do TEE=$(grep -oE '\"busId\": *\"[A-Za-z0-9]+\"' /fixed-fleet.json | grep -oE '[A-Za-z0-9]+\"$' | tr -d '\"' | awk '{printf \"[f=flv]rtmp://mediamtx:1935/%s_1|\", $0}' | sed 's/|$//'); ffmpeg -re -stream_loop -1 -i /video.mp4 -c:v libx264 -preset veryfast -an -f tee \"$TEE\"; sleep 2; done"]
