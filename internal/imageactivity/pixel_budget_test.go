package imageactivity

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"hash/crc32"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

// Only the header dimensions are inspected by the service. Keep these fixtures
// tiny rather than allocating or decoding the advertised pixel surfaces.
func pngPixelHeader(t *testing.T, width, height int) []byte {
	t.Helper()
	var body bytes.Buffer
	if err := png.Encode(&body, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	raw := body.Bytes()
	binary.BigEndian.PutUint32(raw[16:20], uint32(width))
	binary.BigEndian.PutUint32(raw[20:24], uint32(height))
	binary.BigEndian.PutUint32(raw[29:33], crc32.ChecksumIEEE(raw[12:29]))
	return raw
}

func TestImageTaskPixelBudget(t *testing.T) {
	image2K := pngPixelHeader(t, 2048, 2048)
	image4K := pngPixelHeader(t, 4096, 4096)
	large := pngPixelHeader(t, 8000, 8000)
	remainder := pngPixelHeader(t, 4096, 759)
	overRemainder := pngPixelHeader(t, 3108865, 1)
	overIndividual := pngPixelHeader(t, 64000001, 1)
	sixteen := make([][]byte, 16)
	indices := make([]int, 16)
	for i := range sixteen {
		sixteen[i], indices[i] = image2K, i
	}
	for _, tc := range []struct {
		name   string
		input  [][]byte
		want   []int
		pixels int64
	}{
		{"sixteen 2K originals", sixteen, indices, 67108864},
		{"four 4K originals", [][]byte{image4K, image4K, image4K, image4K}, []int{0, 1, 2, 3}, 67108864},
		{"skip overflow and keep exact remainder", [][]byte{large, overRemainder, []byte("<svg/>"), remainder}, []int{0, 3}, 67108864},
		{"single image ceiling", [][]byte{overIndividual, large}, []int{1}, 64000000},
		{"no valid images", [][]byte{overIndividual, []byte("invalid")}, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			items := make([]map[string]string, len(tc.input))
			for i, raw := range tc.input {
				items[i] = map[string]string{"b64": base64.StdEncoding.EncodeToString(raw)}
			}
			body, err := json.Marshal(map[string]any{"data": items})
			if err != nil {
				t.Fatal(err)
			}
			snapshot := upstreamSnapshot{adapter: Adapter{Response: ResponseAdapter{ImagesPointer: "/data", Base64Pointer: ptr("/b64")}}}
			got := (&Service{}).extractImages(t.Context(), taskRow{n: len(tc.input)}, snapshot, body, testNow+60)
			if len(got) != len(tc.want) {
				t.Fatalf("accepted %d images, want %d", len(got), len(tc.want))
			}
			var pixels int64
			for i, img := range got {
				if !bytes.Equal(img.data, tc.input[tc.want[i]]) || img.mime != "image/png" {
					t.Fatalf("original %d changed", i)
				}
				pixels += img.pixels
			}
			if pixels != tc.pixels {
				t.Fatalf("accepted %d pixels, want %d", pixels, tc.pixels)
			}
		})
	}
}

func TestImageTaskPixelBudgetPickupAndSettlement(t *testing.T) {
	for _, source := range []string{"base64", "download"} {
		t.Run(source, func(t *testing.T) {
			f := newFixture(t)
			f.configure(t)
			if source == "download" {
				settings := f.settings
				settings.ExpectedRevision = "2"
				settings.Secret = SecretInput{Mode: "keep"}
				settings.ImageOrigins = []string{f.upstream.server.URL}
				if _, err := f.service.PutUpstream(f.ctx(f.admin), f.admin, f.key(), settings); err != nil {
					t.Fatal(err)
				}
			}
			routes := muxRoutes{mux: http.NewServeMux(), user: f.user, admin: f.admin}
			if err := RegisterRoutes(routes, routes, f.service); err != nil {
				t.Fatal(err)
			}
			setImages := func(images ...[]byte) {
				f.upstream.mu.Lock()
				defer f.upstream.mu.Unlock()
				f.upstream.resultImages = make([]map[string]string, len(images))
				f.upstream.imageBodies = make(map[string][]byte)
				for i, raw := range images {
					if source == "base64" {
						f.upstream.resultImages[i] = map[string]string{"b64": base64.StdEncoding.EncodeToString(raw)}
					} else {
						path := "/images/" + strconv.Itoa(i)
						f.upstream.imageBodies[path] = raw
						f.upstream.resultImages[i] = map[string]string{"url": f.upstream.server.URL + path}
					}
				}
			}
			large, small := pngPixelHeader(t, 8000, 8000), f.upstream.png
			oversized := pngPixelHeader(t, 8001, 8000)
			setImages(large, pngPixelHeader(t, 4000, 4000), small, oversized)
			task := f.submit(t, f.user, 4)
			f.wait(t, func() bool {
				result, err := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
				return err == nil && result.Status == "succeeded"
			})
			result, err := f.service.GetTask(f.ctx(f.user), f.user, task.ID)
			if err != nil || result.ActualImages != 2 || len(result.Images) != 2 || !result.ResultAvailable || result.Charge != (Price{"8", "4"}) || result.Refund != zeroPrice() {
				t.Fatalf("partial result %+v: %v", result, err)
			}
			for index, original := range [][]byte{large, small} {
				req := httptest.NewRequest(http.MethodGet, userPrefix+"/tasks/"+task.ID+"/images/"+strconv.Itoa(index), nil).WithContext(f.ctx(f.user))
				rec := httptest.NewRecorder()
				routes.mux.ServeHTTP(rec, req)
				if rec.Code != http.StatusOK || rec.Header().Get("Content-Type") != "image/png" || !bytes.Equal(rec.Body.Bytes(), original) {
					t.Fatalf("original pickup %d status=%d", index, rec.Code)
				}
			}
			setImages(oversized)
			failed := f.submit(t, f.user, 1)
			f.wait(t, func() bool {
				result, err := f.service.GetTask(f.ctx(f.user), f.user, failed.ID)
				return err == nil && result.Status == "failed"
			})
			result, err = f.service.GetTask(f.ctx(f.user), f.user, failed.ID)
			if err != nil || result.BillingState != "refunded" || result.ActualImages != 0 || result.ResultAvailable || result.Charge != zeroPrice() || result.Refund != (Price{"2", "1"}) {
				t.Fatalf("invalid result refund %+v: %v", result, err)
			}
			wallet, err := f.limited.Wallet(f.ctx(f.user), f.user)
			if err != nil || wallet.Paper != "92" || wallet.Brush != "96" {
				t.Fatalf("wallet %+v: %v", wallet, err)
			}
			f.checkLedger(t)
		})
	}
}
