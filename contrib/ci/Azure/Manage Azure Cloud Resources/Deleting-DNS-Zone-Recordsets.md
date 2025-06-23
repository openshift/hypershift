## General
Recently, we hit the max limit of recordsets on our CI DNS zone. AFAIK, there is no way to filter the recordsets by older/oldest - so you could delete allrecordsets before a certain date - and Azure does not timestamp them in anyway. Running this script will likely clobber any running e2e tests that are currently active so the user should be aware of this fact.

I used ChatGPT to create some bash/shell script commands to delete the recordsets that are not NS or SOA types.

## Set constants
Set a few constants used in the script commands below. This is just an example so you may need to adjust your constants differently.
```
RESOURCE_GROUP="os4-common" // Resource group the DNS Zone lives in 
ZONE_NAME="<dns-zone-name>" // Name of your DNS Zone with the recordsets
RECORDS_FILE="temp.json" // Assumes the file already exists
```

## Create a file of recordsets to delete

This command creates a JSON output of the recordsets that would be deleted. It is a good idea to review them and make sure you aren't going to delete something that shouldn't be.
```
az network dns record-set list \
  --resource-group "$RESOURCE_GROUP" \
  --zone-name "$ZONE_NAME" \
  --output json | \
  jq '[.[] | select(.type | endswith("/SOA") | not) | select(.type | endswith("/NS") | not) | {name: .name, type: (.type | split("/")[-1])}]' > "$RECORDS_FILE"
```

## Test deleting one recordset 
From here, you can run the following command to just delete the first recordset so that you can see how these commands work.
```
COUNT=0

while read -r record; do
  NAME=$(echo "$record" | jq -r '.name')
  TYPE=$(echo "$record" | jq -r '.type' | tr '[:upper:]' '[:lower:]')
  echo "Deleting $TYPE $NAME..."
  az network dns record-set "$TYPE" delete \
    --resource-group "$RESOURCE_GROUP" \
    --zone-name "$ZONE_NAME" \
    --name "$NAME" \
    --yes
  COUNT=$((COUNT + 1))
  if [ "$COUNT" -eq 1 ]; then
    echo "Deleted one record. Stopping test run."
    break
  fi
done < <(jq -c '.[]' "$RECORDS_FILE")
```

Here's the output of running that command:
```
Deleting txt a-api-create-cluster-cbbmt-external-dns...
Deleted one record. Stopping test run.
```

## Delete all recordsets from a file
Once you are satisfied, you can run this command to delete everything in your `$RECORDS_FILE`
```
while read -r record; do
  NAME=$(echo "$record" | jq -r '.name')
  TYPE=$(echo "$record" | jq -r '.type' | tr '[:upper:]' '[:lower:]')
  echo "Deleting $TYPE $NAME..."
  az network dns record-set "$TYPE" delete \
    --resource-group "$RESOURCE_GROUP" \
    --zone-name "$ZONE_NAME" \
    --name "$NAME" \
    --yes
done < <(jq -c '.[]' "$RECORDS_FILE")
```