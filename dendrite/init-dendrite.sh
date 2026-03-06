#!/usr/bin/env bash
set -euo pipefail

CONFIG_DIR="/etc/dendrite"
DATA_DIR="/var/dendrite"
KEY_FILE="${CONFIG_DIR}/matrix_key.pem"
CONFIG_FILE="${CONFIG_DIR}/dendrite.yaml"
TEMPLATE_FILE="/dendrite/dendrite.yaml.template"

DENDRITE_URL="http://localhost:8008"
ADMIN_USER="@admin:${DENDRITE_SERVER_NAME}"

echo "==> Initializing Dendrite..."

mkdir -p "${CONFIG_DIR}" "${DATA_DIR}/media" "${DATA_DIR}/jetstream" "${DATA_DIR}/searchindex"

if [ ! -f "${KEY_FILE}" ]; then
    echo "==> Generating signing key..."
    /usr/bin/generate-keys --private-key "${KEY_FILE}"
    echo "==> Signing key generated at ${KEY_FILE}"
else
    echo "==> Signing key already exists"
fi

echo "==> Applying configuration template..."
envsubst < "${TEMPLATE_FILE}" > "${CONFIG_FILE}"
echo "==> Configuration written to ${CONFIG_FILE}"

wait_for_dendrite() {
    echo "==> Waiting for Dendrite to be ready..."
    for i in $(seq 1 30); do
        if curl -sf "${DENDRITE_URL}/_matrix/client/versions" > /dev/null 2>&1; then
            echo "==> Dendrite is ready"
            return 0
        fi
        sleep 2
    done
    echo "==> Dendrite did not become ready in time"
    return 1
}

create_room_if_not_exists() {
    local alias="$1"
    local name="$2"
    local topic="$3"
    local visibility="${4:-public}"
    local token="$5"

    local full_alias="#${alias}:${DENDRITE_SERVER_NAME}"

    existing=$(curl -sf \
        "${DENDRITE_URL}/_matrix/client/v3/directory/room/${full_alias}" \
        2>/dev/null || true)

    if echo "${existing}" | grep -q "room_id"; then
        echo "    Room ${full_alias} already exists"
        return 0
    fi

    local preset="public_chat"
    if [ "${visibility}" = "private" ]; then
        preset="private_chat"
    fi

    echo "    Creating room ${full_alias}..."
    curl -sf -X POST \
        -H "Authorization: Bearer ${token}" \
        -H "Content-Type: application/json" \
        -d "{
            \"room_alias_name\": \"${alias}\",
            \"name\": \"${name}\",
            \"topic\": \"${topic}\",
            \"preset\": \"${preset}\",
            \"visibility\": \"${visibility}\"
        }" \
        "${DENDRITE_URL}/_matrix/client/v3/createRoom" > /dev/null

    echo "    Room ${full_alias} created"
}

register_admin() {
    echo "==> Registering admin user..."
    local nonce
    nonce=$(curl -sf "${DENDRITE_URL}/_dendrite/admin/registrationToken" \
        -H "Authorization: Bearer ${DENDRITE_REGISTRATION_SECRET}" 2>/dev/null || true)

    result=$(curl -sf -X POST \
        -H "Content-Type: application/json" \
        -d "{
            \"username\": \"admin\",
            \"password\": \"${DENDRITE_ADMIN_PASSWORD}\",
            \"admin\": true
        }" \
        "${DENDRITE_URL}/_dendrite/admin/createAccount" \
        -H "Authorization: Bearer ${DENDRITE_REGISTRATION_SECRET}" 2>/dev/null || true)

    if echo "${result}" | grep -q "user_id"; then
        echo "==> Admin user created"
    else
        echo "==> Admin user may already exist (continuing)"
    fi
}

login_admin() {
    local result
    result=$(curl -sf -X POST \
        -H "Content-Type: application/json" \
        -d "{
            \"type\": \"m.login.password\",
            \"identifier\": {
                \"type\": \"m.id.user\",
                \"user\": \"admin\"
            },
            \"password\": \"${DENDRITE_ADMIN_PASSWORD}\"
        }" \
        "${DENDRITE_URL}/_matrix/client/v3/login")

    echo "${result}" | grep -o '"access_token":"[^"]*"' | cut -d'"' -f4
}

if [ "${1:-}" = "--setup-rooms" ]; then
    wait_for_dendrite

    register_admin

    echo "==> Logging in as admin..."
    TOKEN=$(login_admin)
    if [ -z "${TOKEN}" ]; then
        echo "==> Failed to obtain admin token"
        exit 1
    fi

    echo "==> Creating default rooms..."
    create_room_if_not_exists "general"  "Generelt"        "Generell diskusjon for klubben"            "public"  "${TOKEN}"
    create_room_if_not_exists "harbour"  "Havna"           "Diskusjon om havna og fasiliteter"         "public"  "${TOKEN}"
    create_room_if_not_exists "events"   "Arrangementer"   "Regattaer, dugnader og sosiale hendelser"  "public"  "${TOKEN}"
    create_room_if_not_exists "forsale"  "Til salgs"       "Kjop og salg mellom medlemmer"             "public"  "${TOKEN}"
    create_room_if_not_exists "styre"    "Styret"          "Privat kanal for styremedlemmer"            "private" "${TOKEN}"

    echo "==> Room setup complete"
fi

echo "==> Dendrite initialization complete"
