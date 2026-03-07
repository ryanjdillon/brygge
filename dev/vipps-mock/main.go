package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

var payments sync.Map

type Payment struct {
	Reference          string `json:"reference"`
	Amount             Amount `json:"amount"`
	PaymentDescription string `json:"paymentDescription"`
	ReturnURL          string `json:"returnUrl"`
	Status             string `json:"status"`
}

type Amount struct {
	Currency string `json:"currency"`
	Value    int    `json:"value"`
}

type CreatePaymentRequest struct {
	Amount             Amount `json:"amount"`
	Reference          string `json:"reference"`
	ReturnURL          string `json:"returnUrl"`
	PaymentDescription string `json:"paymentDescription"`
}

func webhookTarget() string {
	if v := os.Getenv("WEBHOOK_TARGET"); v != "" {
		return v
	}
	return "http://api:8080/api/v1/webhooks/vipps"
}

func main() {
	mux := http.NewServeMux()

	// Login API
	mux.HandleFunc("GET /access-management-1.0/access/oauth2/auth", handleAuth)
	mux.HandleFunc("POST /access-management-1.0/access/oauth2/token", handleToken)
	mux.HandleFunc("GET /vipps-userinfo-api/userinfo", handleUserInfo)

	// ePayment API
	mux.HandleFunc("POST /accesstoken/get", handleAccessToken)
	mux.HandleFunc("POST /epayment/v1/payments", handleCreatePayment)
	mux.HandleFunc("GET /mock/approve", handleApprove)
	mux.HandleFunc("POST /mock/approve", handleApproveAction)
	mux.HandleFunc("GET /epayment/v1/payments/{reference}", handleGetPayment)
	mux.HandleFunc("POST /epayment/v1/payments/{reference}/capture", handleCapture)
	mux.HandleFunc("POST /epayment/v1/payments/{reference}/cancel", handleCancel)

	handler := logMiddleware(mux)

	log.Println("Vipps mock server starting on :8090")
	if err := http.ListenAndServe(":8090", handler); err != nil {
		log.Fatal(err)
	}
}

func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.String())
		next.ServeHTTP(w, r)
	})
}

// --- Login API ---

var authPageTmpl = template.Must(template.New("auth").Parse(`<!DOCTYPE html>
<html lang="no">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Vipps Mock Login</title>
<style>
  body { font-family: system-ui, sans-serif; max-width: 420px; margin: 60px auto; padding: 0 20px; background: #f5f5f5; }
  h1 { color: #ff5b24; text-align: center; }
  p { text-align: center; color: #666; }
  .buttons { display: flex; flex-direction: column; gap: 12px; margin-top: 24px; }
  a { display: block; text-align: center; padding: 14px; border-radius: 8px; text-decoration: none; font-size: 16px; font-weight: 600; }
  .admin { background: #ff5b24; color: #fff; }
  .member { background: #fff; color: #ff5b24; border: 2px solid #ff5b24; }
  a:hover { opacity: 0.85; }
</style>
</head>
<body>
  <h1>Vipps Mock</h1>
  <p>Velg testbruker for innlogging</p>
  <div class="buttons">
    <a class="admin" href="{{.RedirectURI}}?code=admin&state={{.State}}">Admin bruker</a>
    <a class="member" href="{{.RedirectURI}}?code=member&state={{.State}}">Vanlig medlem</a>
  </div>
</body>
</html>`))

func handleAuth(w http.ResponseWriter, r *http.Request) {
	redirectURI := r.URL.Query().Get("redirect_uri")
	state := r.URL.Query().Get("state")

	log.Printf("  auth: redirect_uri=%s state=%s", redirectURI, state)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	authPageTmpl.Execute(w, map[string]string{
		"RedirectURI": redirectURI,
		"State":       state,
	})
}

func handleToken(w http.ResponseWriter, r *http.Request) {
	code := r.FormValue("code")
	log.Printf("  token exchange: code=%s", code)

	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": "mock-access-" + code,
		"token_type":   "Bearer",
		"expires_in":   3600,
		"id_token":     "mock-id-token",
		"scope":        "openid name email phoneNumber address",
	})
}

var testUsers = map[string]map[string]any{
	"admin": {
		"sub":          "vipps-admin-001",
		"name":         "Admin Testbruker",
		"email":        "admin@brygge.local",
		"phone_number": "+4799999999",
		"address": map[string]string{
			"street_address": "Havnegata 1",
			"postal_code":    "0150",
			"region":         "Oslo",
			"country":        "NO",
		},
	},
	"member": {
		"sub":          "vipps-member-001",
		"name":         "Medlem Testbruker",
		"email":        "member@brygge.local",
		"phone_number": "+4788888888",
		"address": map[string]string{
			"street_address": "Sjøgata 5",
			"postal_code":    "0151",
			"region":         "Oslo",
			"country":        "NO",
		},
	},
}

func handleUserInfo(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	userID := strings.TrimPrefix(auth, "Bearer mock-access-")

	log.Printf("  userinfo: user_id=%s", userID)

	user, ok := testUsers[userID]
	if !ok {
		http.Error(w, `{"error": "unknown user"}`, http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, user)
}

// --- ePayment API ---

func handleAccessToken(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"token_type":   "Bearer",
		"access_token": "mock-epayment-token",
		"expires_in":   3600,
	})
}

func handleCreatePayment(w http.ResponseWriter, r *http.Request) {
	var req CreatePaymentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, `{"error": "invalid json"}`, http.StatusBadRequest)
		return
	}

	p := Payment{
		Reference:          req.Reference,
		Amount:             req.Amount,
		PaymentDescription: req.PaymentDescription,
		ReturnURL:          req.ReturnURL,
		Status:             "CREATED",
	}
	payments.Store(req.Reference, p)

	log.Printf("  payment created: ref=%s amount=%d %s", req.Reference, req.Amount.Value, req.Amount.Currency)

	redirectURL := fmt.Sprintf("http://localhost:8090/mock/approve?reference=%s&returnUrl=%s", req.Reference, req.ReturnURL)
	writeJSON(w, http.StatusCreated, map[string]string{
		"reference":   req.Reference,
		"redirectUrl": redirectURL,
	})
}

var approvePageTmpl = template.Must(template.New("approve").Parse(`<!DOCTYPE html>
<html lang="no">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Vipps Mock Betaling</title>
<style>
  body { font-family: system-ui, sans-serif; max-width: 420px; margin: 60px auto; padding: 0 20px; background: #f5f5f5; }
  h1 { color: #ff5b24; text-align: center; }
  .card { background: #fff; border-radius: 12px; padding: 24px; margin: 24px 0; box-shadow: 0 2px 8px rgba(0,0,0,0.08); }
  .row { display: flex; justify-content: space-between; padding: 8px 0; border-bottom: 1px solid #eee; }
  .row:last-child { border: none; }
  .label { color: #666; }
  .value { font-weight: 600; }
  .buttons { display: flex; gap: 12px; margin-top: 24px; }
  button { flex: 1; padding: 14px; border-radius: 8px; border: none; font-size: 16px; font-weight: 600; cursor: pointer; }
  .approve { background: #2bb54b; color: #fff; }
  .reject { background: #fff; color: #e44; border: 2px solid #e44; }
  button:hover { opacity: 0.85; }
</style>
</head>
<body>
  <h1>Vipps Mock</h1>
  <div class="card">
    <div class="row"><span class="label">Referanse</span><span class="value">{{.Reference}}</span></div>
    <div class="row"><span class="label">Beskrivelse</span><span class="value">{{.Description}}</span></div>
    <div class="row"><span class="label">Beløp</span><span class="value">{{.DisplayAmount}} {{.Currency}}</span></div>
  </div>
  <form method="POST" action="/mock/approve">
    <input type="hidden" name="reference" value="{{.Reference}}">
    <input type="hidden" name="returnUrl" value="{{.ReturnURL}}">
    <div class="buttons">
      <button class="approve" type="submit" name="action" value="approve">Godkjenn betaling</button>
      <button class="reject" type="submit" name="action" value="reject">Avvis betaling</button>
    </div>
  </form>
</body>
</html>`))

func handleApprove(w http.ResponseWriter, r *http.Request) {
	reference := r.URL.Query().Get("reference")
	returnURL := r.URL.Query().Get("returnUrl")

	val, ok := payments.Load(reference)
	if !ok {
		http.Error(w, "payment not found", http.StatusNotFound)
		return
	}
	p := val.(Payment)

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	approvePageTmpl.Execute(w, map[string]any{
		"Reference":     p.Reference,
		"Description":   p.PaymentDescription,
		"DisplayAmount": fmt.Sprintf("%.2f", float64(p.Amount.Value)/100),
		"Currency":      p.Amount.Currency,
		"ReturnURL":     returnURL,
	})
}

func handleApproveAction(w http.ResponseWriter, r *http.Request) {
	reference := r.FormValue("reference")
	returnURL := r.FormValue("returnUrl")
	action := r.FormValue("action")

	val, ok := payments.Load(reference)
	if !ok {
		http.Error(w, "payment not found", http.StatusNotFound)
		return
	}
	p := val.(Payment)

	if action == "approve" {
		p.Status = "AUTHORIZED"
		payments.Store(reference, p)
		fireWebhook(p, "epayments.payment.authorized.v1", true)
		log.Printf("  payment approved: ref=%s", reference)
	} else {
		p.Status = "ABORTED"
		payments.Store(reference, p)
		fireWebhook(p, "epayments.payment.aborted.v1", false)
		log.Printf("  payment rejected: ref=%s", reference)
	}

	http.Redirect(w, r, returnURL, http.StatusSeeOther)
}

func handleGetPayment(w http.ResponseWriter, r *http.Request) {
	reference := r.PathValue("reference")
	val, ok := payments.Load(reference)
	if !ok {
		http.Error(w, `{"error": "payment not found"}`, http.StatusNotFound)
		return
	}
	p := val.(Payment)

	log.Printf("  get payment: ref=%s status=%s", reference, p.Status)

	writeJSON(w, http.StatusOK, map[string]any{
		"reference": p.Reference,
		"amount":    p.Amount,
		"status":    p.Status,
	})
}

func handleCapture(w http.ResponseWriter, r *http.Request) {
	reference := r.PathValue("reference")
	val, ok := payments.Load(reference)
	if !ok {
		http.Error(w, `{"error": "payment not found"}`, http.StatusNotFound)
		return
	}
	p := val.(Payment)
	p.Status = "CAPTURED"
	payments.Store(reference, p)

	fireWebhook(p, "epayments.payment.captured.v1", true)
	log.Printf("  payment captured: ref=%s", reference)

	writeJSON(w, http.StatusOK, map[string]any{
		"reference": p.Reference,
		"status":    p.Status,
	})
}

func handleCancel(w http.ResponseWriter, r *http.Request) {
	reference := r.PathValue("reference")
	val, ok := payments.Load(reference)
	if !ok {
		http.Error(w, `{"error": "payment not found"}`, http.StatusNotFound)
		return
	}
	p := val.(Payment)
	p.Status = "CANCELLED"
	payments.Store(reference, p)

	fireWebhook(p, "epayments.payment.cancelled.v1", false)
	log.Printf("  payment cancelled: ref=%s", reference)

	writeJSON(w, http.StatusOK, map[string]any{
		"reference": p.Reference,
		"status":    p.Status,
	})
}

func fireWebhook(p Payment, eventName string, success bool) {
	payload := map[string]any{
		"msn":          "mock-msn",
		"reference":    p.Reference,
		"pspReference": "mock-psp-" + p.Reference,
		"name":         eventName,
		"amount": map[string]any{
			"currency": p.Amount.Currency,
			"value":    p.Amount.Value,
		},
		"timestamp": time.Now().UTC().Format(time.RFC3339),
		"success":   success,
	}

	body, _ := json.Marshal(payload)
	target := webhookTarget()

	go func() {
		resp, err := http.Post(target, "application/json", bytes.NewReader(body))
		if err != nil {
			log.Printf("  webhook failed: %s %v", target, err)
			return
		}
		resp.Body.Close()
		log.Printf("  webhook sent: %s -> %s (status %d)", eventName, target, resp.StatusCode)
	}()
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
