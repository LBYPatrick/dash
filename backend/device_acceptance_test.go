package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	dashv2 "dash.local/backend/internal/mcaptypes/dashv2"
	foxglove "dash.local/backend/internal/mcaptypes/foxglove"
	"github.com/foxglove/mcap/go/mcap"
	"google.golang.org/protobuf/proto"
)

// TestPhysicalDeviceExports is explicitly enabled for a short real session.
// It does not modify firmware state, originals, the index or cloud destinations.
func TestPhysicalDeviceExports(t *testing.T) {
	if os.Getenv("DASH_DEVICE_ACCEPTANCE") != "1" {
		t.Skip("opt-in physical device acceptance")
	}
	origin, drive, folder := os.Getenv("DASH_TEST_URL"), os.Getenv("DASH_DEVICE_DRIVE"), os.Getenv("DASH_DEVICE_OUTPUT")
	if origin == "" || !segmentPattern.MatchString(drive+"--0") || !filepath.IsAbs(folder) {
		t.Fatal("explicit URL, drive and absolute private output directory required")
	}
	if err := os.MkdirAll(folder, 0700); err != nil {
		t.Fatal(err)
	}
	client := physicalClient(t, 10*time.Minute)
	for _, format := range []string{"zip", "raw", "mp4", "mcap"} {
		t.Run(format, func(t *testing.T) {
			response, err := client.Get(origin + "/api/info")
			if err != nil {
				t.Fatal(err)
			}
			var info struct {
				Driving    string `json:"driving"`
				Simulation bool   `json:"simulation"`
			}
			err = json.NewDecoder(response.Body).Decode(&info)
			response.Body.Close()
			if err != nil || info.Simulation || info.Driving != "parked" {
				t.Fatal("real device must remain confirmed parked", err)
			}
			body, _ := json.Marshal(ExportRequest{Drive: drive, Format: format, Camera: "qcamera.ts", Stream: true})
			response, err = client.Post(origin+"/api/exports", "application/json", bytes.NewReader(body))
			if err != nil {
				t.Fatal(err)
			}
			var job ExportJob
			err = json.NewDecoder(response.Body).Decode(&job)
			response.Body.Close()
			if err != nil || response.StatusCode != 202 {
				t.Fatal("submit", response.StatusCode, err)
			}
			if format == "mcap" && job.Streaming {
				t.Fatal("MCAP must finish on the device before downloading")
			}
			deadline := time.Now().Add(8 * time.Minute)
			for !job.Streaming && job.Status != "complete" {
				if job.Status == "failed" || job.Status == "canceled" || time.Now().After(deadline) {
					t.Fatal("device conversion did not complete", job.Status, job.Message)
				}
				time.Sleep(500 * time.Millisecond)
				response, err = client.Get(origin + "/api/exports/" + job.ID)
				if err != nil {
					t.Fatal(err)
				}
				err = json.NewDecoder(response.Body).Decode(&job)
				response.Body.Close()
				if err != nil || response.StatusCode != 200 {
					t.Fatal("conversion status", response.StatusCode, err)
				}
			}
			response, err = client.Get(origin + "/api/exports/" + job.ID + "/download")
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			if response.StatusCode != 200 {
				data, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
				t.Fatalf("download %d: %s", response.StatusCode, data)
			}
			if !job.Streaming && response.ContentLength != job.Bytes {
				t.Fatal("completed export has an incorrect download size", response.ContentLength, job.Bytes)
			}
			filename := filepath.Join(folder, "session."+format)
			file, err := os.OpenFile(filename, os.O_CREATE|os.O_TRUNC|os.O_RDWR, 0600)
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			size, err := io.Copy(file, io.LimitReader(response.Body, (512<<20)+1))
			if err != nil || size == 0 || size > 512<<20 {
				t.Fatal("bounded download", size, err)
			}
			if _, err = file.Seek(0, io.SeekStart); err != nil {
				t.Fatal(err)
			}
			switch format {
			case "mp4":
				output, e := exec.Command("ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=codec_name:format=duration", "-of", "csv=p=0", filename).CombinedOutput()
				if e != nil || !bytes.Contains(output, []byte("h264")) {
					t.Fatalf("invalid MP4: %v %s", e, output)
				}
			case "zip":
				reader, err := zip.NewReader(file, size)
				if err != nil {
					t.Fatal(err)
				}
				if len(reader.File) < 6 {
					t.Fatal("missing original files")
				}
				for _, item := range reader.File {
					r, e := item.Open()
					if e != nil {
						t.Fatal(e)
					}
					_, e = io.Copy(io.Discard, r)
					r.Close()
					if e != nil {
						t.Fatal("ZIP checksum", e)
					}
				}
			case "mcap":
				reader, err := mcap.NewReader(file)
				if err != nil {
					t.Fatal(err)
				}
				defer reader.Close()
				metadata, err := reader.Info()
				if err != nil {
					t.Fatal(err)
				}
				for _, attachment := range metadata.AttachmentIndexes {
					if strings.HasSuffix(attachment.Name, "/qcamera.ts") {
						t.Fatal("MCAP retained the low-resolution preview attachment")
					}
				}
				for _, channel := range metadata.Channels {
					if channel.Topic == "/dash/camera/preview/video" || channel.Metadata["source"] == "qcamera.ts" {
						t.Fatal("MCAP retained a preview video channel")
					}
				}
				iterator, err := reader.Messages()
				if err != nil {
					t.Fatal(err)
				}
				frames, typed := 0, 0
				var first, last uint64
				for {
					schema, channel, message, e := iterator.Next(nil)
					if e == io.EOF {
						break
					}
					if e != nil {
						t.Fatal(e)
					}
					if first == 0 || message.LogTime < first {
						first = message.LogTime
					}
					if message.LogTime > last {
						last = message.LogTime
					}
					if schema != nil && schema.Name == "foxglove.CompressedVideo" {
						var frame foxglove.CompressedVideo
						if e = proto.Unmarshal(message.Data, &frame); e != nil || len(frame.Data) == 0 {
							t.Fatal("invalid compressed video", e)
						}
						frames++
					} else if schema != nil {
						if schema.Name == "dash.v2.CameraFrameIndex" {
							var frame dashv2.CameraFrameIndex
							if e := proto.Unmarshal(message.Data, &frame); e != nil || frame.Camera == "preview" {
								t.Fatal("MCAP retained a structured preview frame index", e)
							}
						}
						if schema.Encoding != "protobuf" || channel.MessageEncoding != "protobuf" {
							t.Fatal("untyped telemetry schema", schema.Name)
						}
						typed++
					}
				}
				if frames == 0 || typed == 0 || time.Duration(last-first) > 2*time.Minute {
					t.Fatal("video, typed topics or time span invalid", frames, typed, time.Duration(last-first))
				}
				t.Logf("MCAP video frames=%d typed messages=%d span=%s", frames, typed, time.Duration(last-first))
			}
			t.Log(fmt.Sprintf("verified %s export: %d bytes", format, size))
		})
	}
}
