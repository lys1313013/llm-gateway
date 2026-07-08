#!/bin/bash
# Start both frontend and backend; Ctrl+C stops both.
DIR="$(dirname "$0")"
cleanup() { kill 0; }
trap cleanup EXIT
bash "$DIR/start-backend.sh" &
bash "$DIR/start-frontend.sh" &
wait