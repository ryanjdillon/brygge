# Answers one delivery-log request from the Brygge API (BRY-581).
#
# Runs per connection on the brygge-mail-log socket, as a dynamic user in
# systemd-journal: the API itself has no journal access. Reads one recipient
# address on stdin and writes, as JSON lines, Stalwart's delivery events for
# that recipient only:
#   - events whose TO names the recipient (submission, attempt start,
#     delivered, RCPT rejected, DSNs, delivery failed), TO reduced to the
#     recipient so co-recipients are not disclosed;
#   - events without TO (connection errors, whole-message rejections,
#     rescheduling, completion) whose SPANID is an attempt to the recipient.
# A final {"__end":true} line marks a complete answer.

IFS= read -r -t 5 rcpt || exit 64
rcpt=${rcpt%$'\r'}
rcpt=${rcpt,,}
plain_address='^[^][[:space:],@"]+@[^][[:space:],@"]+$'
if [[ ! $rcpt =~ $plain_address ]]; then
  echo "mail-log-helper: not a plain address" >&2
  exit 64
fi

# Stalwart 0.15 event descriptions; keep in step with mail_journal.go.
matches=(
  "MESSAGE=Queued message submission for delivery"
  "MESSAGE=Delivery attempt started"
  "MESSAGE=Message delivered"
  "MESSAGE=DSN success notification"
  "MESSAGE=SMTP RCPT TO rejected"
  "MESSAGE=SMTP RCPT TO failed"
  "MESSAGE=Message rejected by remote server"
  "MESSAGE=Connection error"
  "MESSAGE=MX record lookup failed"
  "MESSAGE=IP address lookup failed"
  "MESSAGE=SMTP greeting failed"
  "MESSAGE=SMTP EHLO rejected"
  "MESSAGE=SMTP MAIL FROM rejected"
  "MESSAGE=STARTTLS error"
  "MESSAGE=Implicit TLS error"
  "MESSAGE=Message rescheduled for delivery"
  "MESSAGE=DSN temporary failure notification"
  "MESSAGE=DSN permanent failure notification"
  "MESSAGE=Delivery failed"
  "MESSAGE=Delivery completed"
)

journalctl --no-pager --quiet -o json --since=-35d \
  --output-fields=__REALTIME_TIMESTAMP,MESSAGE,SPANID,QUEUEID,TO,HOSTNAME,CODE,DETAILS,REASON,CAUSEDBY,NEXTRETRY \
  _SYSTEMD_UNIT=stalwart.service "${matches[@]}" |
  jq -c -n --arg r "$rcpt" '
    def str: if type == "string" then . else null end;
    def names_rcpt: (.TO | str // "") | ascii_downcase
      | [splits("[\\[\\],\\s]+")] | any(. == $r);
    [inputs | with_entries(.value |= str)] as $all
    | ($all | map(select(.MESSAGE == "Delivery attempt started" and names_rcpt) | .SPANID)
            | map(select(. != null)) | unique) as $spans
    | $all[]
    | if .TO != null then
        select(names_rcpt) | .TO = $r
      else
        select(.MESSAGE != "Queued message submission for delivery"
               and .SPANID != null and (.SPANID as $s | $spans | index($s)) != null)
      end
    | {__REALTIME_TIMESTAMP, MESSAGE, SPANID, QUEUEID, TO, HOSTNAME, CODE, DETAILS, REASON, CAUSEDBY, NEXTRETRY}
    | with_entries(select(.value != null))'

echo '{"__end":true}'
