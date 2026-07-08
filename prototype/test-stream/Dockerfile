FROM jrottenberg/ffmpeg:4.4-alpine
COPY 142755-780943401_medium.mp4 /video.mp4
ENTRYPOINT ["sh", "-c"]
CMD ["while true; do ffmpeg -re -stream_loop -1 -i /video.mp4 -map 0:v -c:v libx264 -preset veryfast -g 30 -an -f flv rtmp://mediamtx:1935/livefeed; sleep 2; done"]
