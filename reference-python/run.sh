#!/bin/sh
# Export the whole Photos library, then upload it with 4 parallel workers,
# then two one-at-a-time retry passes for leftovers. Keeps the Mac awake.
cd "$HOME/PhotosExport"
echo "export started $(date)"
osxphotos export "$HOME/PhotosExport/full" --directory "{created.year}/{created.mm}" \
  --skip-bursts --exiftool --touch-file --update --no-progress
echo "export finished $(date) exit=$?"
python3 make_items.py
python3 driver.py plan
for k in 0 1 2 3; do python3 driver.py worker $k 4 & done
wait
for pass in 1 2; do
  echo "retry pass $pass $(date)"
  python3 driver.py plan
  rm -f state-w1.json state-w2.json state-w3.json
  python3 driver.py worker 0 1
done
echo "upload finished $(date)"
