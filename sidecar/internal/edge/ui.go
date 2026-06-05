package edge

import (
	"encoding/json"
	"net/http"
)

// UIHandler serves the local edge config UI (a single page + a tiny JSON API).
// Bound to loopback by the caller. Secrets (token, Proton password) are never
// echoed back to the page — only "set" flags — and are kept when re-saving blank.
func UIHandler(runner *Runner, cfgPath string) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write([]byte(configPage))
	})

	mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			cfg, _ := LoadConfig(cfgPath)
			writeJSONEdge(w, http.StatusOK, map[string]any{
				"server":              cfg.Server,
				"token_set":           cfg.Token != "",
				"folder":              cfg.Folder,
				"proton_user":         cfg.ProtonUser,
				"proton_password_set": cfg.ProtonPassword != "",
				"proton_mailbox":      cfg.ProtonMailbox,
				"interval_secs":       cfg.IntervalSecs,
			})
		case http.MethodPost:
			var in struct {
				Server, Token, Folder           string
				ProtonUser, ProtonPassword      string
				ProtonMailbox                   string
				IntervalSecs                    int
			}
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				writeJSONEdge(w, http.StatusBadRequest, map[string]string{"error": "invalid JSON"})
				return
			}
			cfg, _ := LoadConfig(cfgPath)
			cfg.Server = in.Server
			cfg.Folder = in.Folder
			cfg.ProtonUser = in.ProtonUser
			cfg.ProtonMailbox = in.ProtonMailbox
			cfg.IntervalSecs = in.IntervalSecs
			if in.Token != "" { // blank = keep existing secret
				cfg.Token = in.Token
			}
			if in.ProtonPassword != "" {
				cfg.ProtonPassword = in.ProtonPassword
			}
			if err := cfg.Save(cfgPath); err != nil {
				writeJSONEdge(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
				return
			}
			writeJSONEdge(w, http.StatusOK, map[string]bool{"ok": true})
		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	})

	mux.HandleFunc("/api/status", func(w http.ResponseWriter, _ *http.Request) {
		writeJSONEdge(w, http.StatusOK, runner.Status())
	})

	mux.HandleFunc("/api/sync", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		writeJSONEdge(w, http.StatusOK, runner.RunOnce(r.Context()))
	})

	return mux
}

func writeJSONEdge(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

const configPage = `<!doctype html><html lang="en"><head><meta charset="utf-8">
<meta name="viewport" content="width=device-width,initial-scale=1">
<title>Hygur — local connectors</title>
<style>
:root{color-scheme:light dark}
body{font:15px/1.5 -apple-system,system-ui,sans-serif;max-width:560px;margin:2rem auto;padding:0 1rem;color:#1b1b18;background:#fbfaf7}
@media(prefers-color-scheme:dark){body{color:#eceae0;background:#16170f}}
h1{font-size:21px;margin:0 0 .25rem}p.sub{color:#74726b;margin:0 0 1.5rem}
fieldset{border:1px solid #d9d7cf;border-radius:10px;margin:0 0 1rem;padding:.75rem 1rem}
@media(prefers-color-scheme:dark){fieldset{border-color:#313127}}
legend{font-weight:600;padding:0 .4rem}
label{display:block;margin:.6rem 0 .2rem;font-size:13px;font-weight:500}
input{width:100%;box-sizing:border-box;padding:.5rem;border:1px solid #d9d7cf;border-radius:8px;background:transparent;color:inherit;font:inherit}
.row{display:flex;gap:.75rem;align-items:center;margin-top:1rem}
button{padding:.55rem 1rem;border:0;border-radius:8px;background:#2e6a57;color:#fff;font:inherit;font-weight:600;cursor:pointer}
button.ghost{background:transparent;color:#2e6a57;border:1px solid #2e6a57}
.hint{color:#74726b;font-size:12px;margin:.2rem 0 0}
#status{margin-top:1rem;padding:.6rem .8rem;border-radius:8px;background:#f0eee7;font-size:13px;white-space:pre-wrap}
@media(prefers-color-scheme:dark){#status{background:#24251b}}
.ok{color:#2e6a57}.err{color:#9b3d2e}
</style></head><body>
<h1>Hygur — connecteurs locaux</h1>
<p class="sub">Pousse tes sources locales (Fichiers, Proton) vers ton instance cloud. Tout reste sur cette machine sauf le texte poussé.</p>
<form id="f">
  <fieldset><legend>Cloud</legend>
    <label>Endpoint</label><input id="server" placeholder="https://cloud.hygur.ai">
    <label>Device token</label><input id="token" type="password" placeholder="(collé une fois)">
    <p class="hint" id="token-hint"></p>
  </fieldset>
  <fieldset><legend>Fichiers</legend>
    <label>Dossier à synchroniser</label><input id="folder" placeholder="/Users/moi/Documents (vide = désactivé)">
    <p class="hint">.txt .md .docx .pdf (couche texte)</p>
  </fieldset>
  <fieldset><legend>Proton Mail (via Proton Bridge)</legend>
    <label>Utilisateur</label><input id="proton_user" placeholder="moi@proton.me (vide = désactivé)">
    <label>Mot de passe Bridge</label><input id="proton_password" type="password" placeholder="(mot de passe d'app Bridge)">
    <p class="hint" id="pp-hint"></p>
    <label>Boîte(s)</label><input id="proton_mailbox" placeholder="All Mail">
  </fieldset>
  <fieldset><legend>Planification</legend>
    <label>Intervalle (secondes, 0 = manuel)</label><input id="interval_secs" type="number" value="0">
  </fieldset>
  <div class="row">
    <button type="submit">Enregistrer</button>
    <button type="button" class="ghost" id="sync">Synchroniser maintenant</button>
  </div>
</form>
<div id="status">Chargement…</div>
<script>
const $=id=>document.getElementById(id);
async function load(){
  const c=await (await fetch('/api/config')).json();
  $('server').value=c.server||''; $('folder').value=c.folder||'';
  $('proton_user').value=c.proton_user||''; $('proton_mailbox').value=c.proton_mailbox||'All Mail';
  $('interval_secs').value=c.interval_secs||0;
  $('token-hint').textContent=c.token_set?'✓ token enregistré (laisse vide pour garder)':'';
  $('pp-hint').textContent=c.proton_password_set?'✓ mot de passe enregistré (laisse vide pour garder)':'';
  refresh();
}
async function refresh(){
  const s=await (await fetch('/api/status')).json();
  let t=s.last_sync_at?('Dernière synchro : '+new Date(s.last_sync_at).toLocaleString()):'Pas encore synchronisé.';
  t+='\n'+(s.running?'⏳ en cours…':('Fichiers poussés : '+(s.files_pushed||0)+' · Mails : '+(s.mail_pushed||0)+' · erreurs : '+(s.errors||0)));
  const st=$('status'); st.textContent=t; st.className=s.last_error?'err':(s.errors?'':'ok');
  if(s.last_error) st.textContent+='\n⚠ '+s.last_error;
}
$('f').addEventListener('submit',async e=>{
  e.preventDefault();
  const body={Server:$('server').value.trim(),Token:$('token').value,Folder:$('folder').value.trim(),
    ProtonUser:$('proton_user').value.trim(),ProtonPassword:$('proton_password').value,
    ProtonMailbox:$('proton_mailbox').value.trim(),IntervalSecs:parseInt($('interval_secs').value||'0',10)};
  const r=await fetch('/api/config',{method:'POST',headers:{'Content-Type':'application/json'},body:JSON.stringify(body)});
  $('token').value='';$('proton_password').value='';
  $('status').textContent=r.ok?'✓ Enregistré.':'Erreur à l’enregistrement.';
  load();
});
$('sync').addEventListener('click',async()=>{
  $('status').textContent='⏳ Synchronisation…';
  const s=await (await fetch('/api/sync',{method:'POST'})).json();
  refresh();
});
load(); setInterval(refresh,5000);
</script></body></html>`
