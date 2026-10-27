# Chemito API §8: history video

Missing from `PMIDTC_CIPLAPIS.xlsx` (that file covers login, GPS, alarms and
live only). Copied from the vendor's doc as received 2026-10-27. Base URL in
the examples is the vendor's; ours is in `vendors.json` (`baseUrl`).

`st` here: `0` = sub stream, `1` = main stream (live video uses the reverse).
`pbt` (information source): `0` = device (default), `1` = server.

Implemented in `Prod/vendorclients/chemitoapi.py`: 8.2 `record_files`,
8.3 `record_hls`, 8.4 `record_flv`, 8.5 `server_flv`, 8.6-8.9 `flv_control`.

## 8.1 Monthly calendar

`GET /api/v1/basic/record/calendar?key&terid&starttime=2017-01-01&st=1&pbt=1`

- `starttime`: the month of this date is the month queried.
- Response `data[]`: `{date: "yyyy-MM-dd", filetype}`. `filetype` 1 = normal
  recording, 2 = alarm recording.

## 8.2 Video file list

`GET /api/v1/basic/record/filelist?key&terid&starttime=2017-01-01 00:00:00&endtime=2017-01-01 23:59:59&chl=1,2,3,4&st=1&pbt=1`

- `chl`: channel ids, comma-separated.
- Response `data[]`: `{name: "0-1-0", chn, filetype, starttime, endtime}`.

## 8.3 Device history stream (HLS)

`GET /api/v1/basic/record/video?key&terid&starttime=2017-06-19 15:20:56&endtime=2017-06-19 16:20:22&chl=1&st=1`

- `chl`: one channel.
- Response `data`: `{url: "https://<host>:28090/play/<terid>/1/20170626002056_20170626003022_main.m3u8"}`.

## 8.4 Device history stream (FLV)

`GET /api/v1/basic/record/flv/video?key&terid&starttime=2023-09-27 15:20:56&chl=1&st=0`

- `chl`: channel ids, comma-separated.
- Response: `data[]: {url: "https://<host>:8091/play.flv?devid&chl&st&audio&starttime&sn&token"}`,
  plus `sn` (stream id, for 8.6-8.9).

## 8.5 Server history stream (FLV)

`GET /api/v1/basic/server/video?key&terid&starttime=2023-09-27 15:20:56&chl=1`

- Playback of the copy stored on the server ("server playback" in the
  vendor's client). No `st`.
- Response: same shape as 8.4, `data[]: {url}` plus `sn`.

## 8.6-8.9 Control a server FLV stream

`POST /api/v1/basic/flv/{pause|resume|seek|stop}`, JSON body
`{"key", "sn"}`; `seek` also takes `"starttime": "2023-10-12 08:30:00"`.
Response `{"errorcode": 200}`.
