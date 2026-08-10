import io
lines = io.open('scripts/extract_relay_model_mapping.py','rb').read().split(b'\n')
ln = lines[145]
print("position 200..230 bytes:", repr(ln[200:230]))
print("position 205..225:", repr(ln[205:225]))
