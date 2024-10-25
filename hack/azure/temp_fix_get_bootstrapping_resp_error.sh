#!/bin/bash

# Define the file path
FILE="pkg/provisionclients/client/operations/node_bootstrapping_get_responses.go"

# Check if the file exists
if [ ! -f "$FILE" ]; then
  echo "File $FILE does not exist."
  exit 1
fi

# Use sed to delete the readResponse() method if it exists
sed -i '/func (o \*NodeBootstrappingGetDefault) readResponse/,/^}/d' "$FILE"

echo "readResponse() method deleted from $FILE if it existed. This is for a temporary fix that is in node_bootstrapping_get_responses_override.go."