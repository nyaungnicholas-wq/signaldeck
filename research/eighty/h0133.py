import sqlite3

conn = sqlite3.connect('file:data/signaldeck.db?mode=ro', uri=True)
# The permitted schema contains no product-safety recall announcement data.
print('INSUFFICIENT=1')
conn.close()