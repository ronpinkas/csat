# Link-builder integrations

Drop-in functions for your call platform to mint a CSAT survey link at end of call. Both produce
a token byte-for-byte compatible with the CSAT server's validation (verified against a live
server). Use the same `crypto_secret` as the deployment (copy it from the admin `/settings` page).

Token = `base64url_nopad( nonce(12B) || AES-256-GCM_seal(key, nonce, "subject|subjectTime|lang") )`,
where `key = SHA-256(crypto_secret)`. `lang` is `en` or `es`. The link is `<base>/s?t=<token>`.

## Python (`mint_link.py`)
Requires `pip install cryptography`.

```python
from mint_link import mint_link

url = mint_link("https://csat.example.com", CRYPTO_SECRET,
                subject="+15551234567", subject_time=1717286400, lang="es")
# then SMS `url` to the customer
```

## Node.js (`mint_link.js`)
No dependencies (uses the built-in `crypto` module).

```js
const { mintLink } = require("./mint_link");

const url = mintLink("https://csat.example.com", CRYPTO_SECRET,
                     "+15551234567", 1717286400, "es");
```

## CLI (handy for testing)
```sh
export CSAT_CRYPTO_SECRET="...the deployment secret..."
python3 mint_link.py --subject +15551234567 --lang es --base https://csat.example.com
node    mint_link.js  --subject +15551234567 --lang es --base https://csat.example.com
```
