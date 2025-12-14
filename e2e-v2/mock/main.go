package main

import (
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

type eniRecord struct {
	ID            string            `json:"eniId"`
	PrivateIP     string            `json:"privateIp"`
	InterfaceType string            `json:"interfaceType"`
	SubnetID      string            `json:"subnetId"`
	Description   string            `json:"description,omitempty"`
	Tags          map[string]string `json:"-"`
}

type eniStore struct {
	mu      sync.RWMutex
	enis    map[string]*eniRecord
	ipIndex map[string]string
}

func newENIStore() *eniStore {
	return &eniStore{
		enis:    make(map[string]*eniRecord),
		ipIndex: make(map[string]string),
	}
}

func (s *eniStore) upsert(eni eniRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()

	copy := eni
	if copy.Tags == nil {
		copy.Tags = make(map[string]string)
	} else {
		copy.Tags = cloneTags(copy.Tags)
	}

	s.enis[copy.ID] = &copy
	s.ipIndex[copy.PrivateIP] = copy.ID
}

func (s *eniStore) byID(id string) (*eniRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rec, ok := s.enis[id]
	if !ok {
		return nil, false
	}
	return cloneENI(rec), true
}

func (s *eniStore) byPrivateIP(ip string) (*eniRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	id, ok := s.ipIndex[ip]
	if !ok {
		return nil, false
	}
	rec, ok := s.enis[id]
	if !ok {
		return nil, false
	}
	return cloneENI(rec), true
}

func (s *eniStore) mergeTags(id string, tags map[string]string) (*eniRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.enis[id]
	if !ok {
		return nil, fmt.Errorf("eni %s not found", id)
	}
	if rec.Tags == nil {
		rec.Tags = make(map[string]string)
	}
	for k, v := range tags {
		rec.Tags[k] = v
	}
	return cloneENI(rec), nil
}

func (s *eniStore) deleteTags(id string, keys []string) (*eniRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	rec, ok := s.enis[id]
	if !ok {
		return nil, fmt.Errorf("eni %s not found", id)
	}
	if len(rec.Tags) == 0 {
		return cloneENI(rec), nil
	}
	for _, k := range keys {
		delete(rec.Tags, k)
	}
	return cloneENI(rec), nil
}

func cloneENI(src *eniRecord) *eniRecord {
	if src == nil {
		return nil
	}
	copy := *src
	copy.Tags = cloneTags(src.Tags)
	return &copy
}

func cloneTags(tags map[string]string) map[string]string {
	if tags == nil {
		return nil
	}
	dup := make(map[string]string, len(tags))
	for k, v := range tags {
		dup[k] = v
	}
	return dup
}

func main() {
	addr := ":" + envOrDefault("PORT", "4566")
	store := newENIStore()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/admin/enis", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			methodNotAllowed(w)
			return
		}
		handleSeedENI(w, r, store)
	})
	mux.HandleFunc("/admin/tags/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			methodNotAllowed(w)
			return
		}
		handleGetTags(w, r, store)
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost, http.MethodGet:
			handleQueryAPI(w, r, store)
		default:
			methodNotAllowed(w)
		}
	})

	srv := &http.Server{
		Addr:    addr,
		Handler: logRequests(mux),
	}

	log.Printf("Starting AWS EC2 mock on %s", addr)
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("server error: %v", err)
	}
}

func handleSeedENI(w http.ResponseWriter, r *http.Request, store *eniStore) {
	var req eniRecord
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Sprintf("invalid JSON: %v", err))
		return
	}

	if req.ID == "" || req.PrivateIP == "" || req.InterfaceType == "" || req.SubnetID == "" {
		writeJSONError(w, http.StatusBadRequest, "eniId, privateIp, interfaceType, and subnetId are required")
		return
	}

	store.upsert(req)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":  "ok",
		"message": "ENI seeded",
	})
}

func handleGetTags(w http.ResponseWriter, r *http.Request, store *eniStore) {
	eniID := strings.TrimPrefix(r.URL.Path, "/admin/tags/")
	if eniID == "" {
		writeJSONError(w, http.StatusBadRequest, "eniId is required")
		return
	}

	rec, ok := store.byID(eniID)
	if !ok {
		writeJSONError(w, http.StatusNotFound, fmt.Sprintf("eni %s not found", eniID))
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rec.Tags)
}

func handleQueryAPI(w http.ResponseWriter, r *http.Request, store *eniStore) {
	if err := r.ParseForm(); err != nil {
		writeXMLError(w, http.StatusBadRequest, "InvalidRequest", fmt.Sprintf("cannot parse request: %v", err))
		return
	}

	action := strings.Title(strings.ToLower(r.Form.Get("Action")))
	switch action {
	case "Describeaccountattributes":
		describeAccountAttributes(w)
	case "Describenetworkinterfaces":
		describeNetworkInterfaces(w, r, store)
	case "Createtags":
		createTags(w, r, store)
	case "Deletetags":
		deleteTags(w, r, store)
	case "":
		writeXMLError(w, http.StatusBadRequest, "InvalidAction", "Action is required")
	default:
		writeXMLError(w, http.StatusBadRequest, "InvalidAction", fmt.Sprintf("unsupported action %s", action))
	}
}

func describeAccountAttributes(w http.ResponseWriter) {
	reqID := requestID()
	w.Header().Set("Content-Type", "text/xml")
	response := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<DescribeAccountAttributesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/">
  <requestId>%s</requestId>
  <accountAttributeSet>
    <item>
      <attributeName>supported-platforms</attributeName>
      <attributeValueSet>
        <item>
          <attributeValue>VPC</attributeValue>
        </item>
      </attributeValueSet>
    </item>
  </accountAttributeSet>
</DescribeAccountAttributesResponse>`, reqID)
	_, _ = w.Write([]byte(response))
}

func describeNetworkInterfaces(w http.ResponseWriter, r *http.Request, store *eniStore) {
	privateIPs := extractFilterValues(r.Form, "private-ip-address")
	if len(privateIPs) == 0 {
		writeXMLError(w, http.StatusBadRequest, "InvalidParameterValue", "private-ip-address filter is required")
		return
	}

	rec, ok := store.byPrivateIP(privateIPs[0])
	if !ok {
		writeXMLError(w, http.StatusBadRequest, "InvalidNetworkInterfaceID.NotFound", fmt.Sprintf("no ENI for IP %s", privateIPs[0]))
		return
	}

	tagSet := buildTagSet(rec.Tags)
	w.Header().Set("Content-Type", "text/xml")
	response := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<DescribeNetworkInterfacesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/">
  <requestId>%s</requestId>
  <networkInterfaceSet>
    <item>
      <networkInterfaceId>%s</networkInterfaceId>
      <subnetId>%s</subnetId>
      <description>%s</description>
      <interfaceType>%s</interfaceType>
      <privateIpAddressesSet>
        <item>
          <privateIpAddress>%s</privateIpAddress>
        </item>
      </privateIpAddressesSet>
      <tagSet>
%s      </tagSet>
    </item>
  </networkInterfaceSet>
</DescribeNetworkInterfacesResponse>`, requestID(), xmlEscape(rec.ID), xmlEscape(rec.SubnetID), xmlEscape(rec.Description), xmlEscape(rec.InterfaceType), xmlEscape(rec.PrivateIP), tagSet)
	_, _ = w.Write([]byte(response))
}

func createTags(w http.ResponseWriter, r *http.Request, store *eniStore) {
	eniID := firstNonEmpty(r.Form.Get("ResourceId.1"), r.Form.Get("ResourceId.0"), r.Form.Get("ResourceId"))
	if eniID == "" {
		writeXMLError(w, http.StatusBadRequest, "InvalidParameterValue", "ResourceId.1 is required")
		return
	}
	tags := extractTags(r.Form)
	if len(tags) == 0 {
		writeXMLError(w, http.StatusBadRequest, "InvalidParameterValue", "at least one Tag.n.Key is required")
		return
	}

	if _, err := store.mergeTags(eniID, tags); err != nil {
		writeXMLError(w, http.StatusBadRequest, "InvalidNetworkInterfaceID.NotFound", err.Error())
		return
	}

	writeSimpleResponse(w, "CreateTagsResponse", "CreateTagsResult")
}

func deleteTags(w http.ResponseWriter, r *http.Request, store *eniStore) {
	eniID := firstNonEmpty(r.Form.Get("ResourceId.1"), r.Form.Get("ResourceId.0"), r.Form.Get("ResourceId"))
	if eniID == "" {
		writeXMLError(w, http.StatusBadRequest, "InvalidParameterValue", "ResourceId.1 is required")
		return
	}
	keys := extractTagKeys(r.Form)
	if len(keys) == 0 {
		writeXMLError(w, http.StatusBadRequest, "InvalidParameterValue", "at least one Tag.n.Key is required")
		return
	}

	if _, err := store.deleteTags(eniID, keys); err != nil {
		writeXMLError(w, http.StatusBadRequest, "InvalidNetworkInterfaceID.NotFound", err.Error())
		return
	}

	writeSimpleResponse(w, "DeleteTagsResponse", "DeleteTagsResult")
}

func extractFilterValues(values map[string][]string, filterName string) []string {
	var results []string
	for key, vals := range values {
		if !strings.HasPrefix(strings.ToLower(key), "filter.") {
			continue
		}

		parts := strings.Split(key, ".")
		if len(parts) < 3 {
			continue
		}
		// filter.<index>.name or filter.<index>.value.<i>
		if strings.EqualFold(parts[2], "name") && len(vals) > 0 && strings.EqualFold(vals[0], filterName) {
			idx := parts[1]
			// look for matching values
			for valueKey, valueVals := range values {
				if strings.HasPrefix(strings.ToLower(valueKey), fmt.Sprintf("filter.%s.value", idx)) {
					results = append(results, valueVals...)
				}
			}
		}
	}
	return results
}

func extractTags(values map[string][]string) map[string]string {
	tags := make(map[string]string)
	for key, vals := range values {
		if !strings.HasPrefix(strings.ToLower(key), "tag.") {
			continue
		}
		parts := strings.Split(key, ".")
		if len(parts) != 3 || !strings.EqualFold(parts[2], "key") {
			continue
		}
		if len(vals) == 0 {
			continue
		}
		idx := parts[1]
		valueKey := fmt.Sprintf("Tag.%s.Value", idx)
		value := first(values[valueKey])
		tags[vals[0]] = value
	}
	return tags
}

func extractTagKeys(values map[string][]string) []string {
	var keys []string
	for key, vals := range values {
		if !strings.HasPrefix(strings.ToLower(key), "tag.") {
			continue
		}
		parts := strings.Split(key, ".")
		if len(parts) != 3 || !strings.EqualFold(parts[2], "key") {
			continue
		}
		keys = append(keys, first(vals))
	}
	return keys
}

func buildTagSet(tags map[string]string) string {
	if len(tags) == 0 {
		return ""
	}
	var b strings.Builder
	for k, v := range tags {
		b.WriteString("        <item>\n")
		b.WriteString(fmt.Sprintf("          <key>%s</key>\n", xmlEscape(k)))
		b.WriteString(fmt.Sprintf("          <value>%s</value>\n", xmlEscape(v)))
		b.WriteString("        </item>\n")
	}
	return b.String()
}

func writeSimpleResponse(w http.ResponseWriter, envelope, result string) {
	w.Header().Set("Content-Type", "text/xml")
	response := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<%s xmlns="http://ec2.amazonaws.com/doc/2016-11-15/">
  <requestId>%s</requestId>
  <%s>
    <return>true</return>
  </%s>
</%s>`, envelope, requestID(), result, result, envelope)
	_, _ = w.Write([]byte(response))
}

func writeXMLError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "text/xml")
	w.WriteHeader(status)
	errResp := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<Response>
  <Errors>
    <Error>
      <Code>%s</Code>
      <Message>%s</Message>
    </Error>
  </Errors>
  <RequestID>%s</RequestID>
</Response>`, xmlEscape(code), xmlEscape(message), requestID())
	_, _ = w.Write([]byte(errResp))
}

func writeJSONError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func methodNotAllowed(w http.ResponseWriter) {
	w.WriteHeader(http.StatusMethodNotAllowed)
}

func first(values []string) string {
	if len(values) == 0 {
		return ""
	}
	return values[0]
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func envOrDefault(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func requestID() string {
	return fmt.Sprintf("req-%d", time.Now().UnixNano())
}

func xmlEscape(s string) string {
	replacer := strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		"\"", "&quot;",
		"'", "&apos;",
	)
	return replacer.Replace(s)
}

func logRequests(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		log.Printf("%s %s %s (%s)", r.Method, r.URL.Path, r.URL.RawQuery, time.Since(start).Round(time.Millisecond))
	})
}

// Ensure strconv is referenced for completeness where form parsing may need ints later.
// Avoids accidental unused import removal when adding future fields.
var _ = strconv.IntSize
