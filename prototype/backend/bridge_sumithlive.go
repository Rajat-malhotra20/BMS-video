package main

import (
	"encoding/json"
	"net/http"

	"mediamtx-console/vendorclients/sumithlive"
)

// sumithLiveRequest resolves a Sumith/Trakzee jspLink. Unlike Castmaster/N9M
// this has no ffmpeg/MediaMTX step: the vendor's own doc never reveals what
// stream protocol the returned jspLink page uses internally, so there's
// nothing to remux — we just hand the embeddable page URL back to the
// frontend to load in an iframe.
type sumithLiveRequest struct {
	BaseURL   string `json:"baseUrl"` // optional, defaults to https://trakzee2.uffizio.com
	Username  string `json:"username"`
	Password  string `json:"password"`
	PlateNo   string `json:"plateNo"`
	ChannelID int    `json:"channelId"`
	ProjectID int    `json:"projectId"`
}

type sumithLiveResponse struct {
	JSPLink string `json:"jspLink"`
}

// handleSumithLiveStart logs into Sumith/Trakzee and resolves the
// embeddable live-video page URL for one vehicle/channel.
func (b *bridgeServer) handleSumithLiveStart(w http.ResponseWriter, r *http.Request) {
	var req sumithLiveRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON body: "+err.Error(), http.StatusBadRequest)
		return
	}
	if req.Username == "" || req.Password == "" || req.PlateNo == "" {
		http.Error(w, "username, password and plateNo are required", http.StatusBadRequest)
		return
	}

	client := sumithlive.NewClient(req.BaseURL, nil)
	token, err := client.GetAccessToken(req.Username, req.Password)
	if err != nil {
		http.Error(w, "sumithlive login failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	link, err := client.GetLiveStreamingLink(token, req.PlateNo, req.ChannelID, req.ProjectID)
	if err != nil {
		http.Error(w, "sumithlive: get live streaming link failed: "+err.Error(), http.StatusBadGateway)
		return
	}

	writeJSON(w, sumithLiveResponse{JSPLink: link})
}

// handleSumithLiveVehicles lists the plates visible to this account, so the
// frontend can populate a picker instead of needing a plate typed in blind.
func (b *bridgeServer) handleSumithLiveVehicles(w http.ResponseWriter, r *http.Request) {
	username, password := r.URL.Query().Get("username"), r.URL.Query().Get("password")
	if username == "" || password == "" {
		http.Error(w, "username and password query params are required", http.StatusBadRequest)
		return
	}
	client := sumithlive.NewClient(r.URL.Query().Get("baseUrl"), nil)
	vehicles, err := client.ListVehicles(username, password)
	if err != nil {
		http.Error(w, "sumithlive: list vehicles failed: "+err.Error(), http.StatusBadGateway)
		return
	}
	writeJSON(w, vehicles)
}
