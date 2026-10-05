import sqlite3
import math
from datetime import datetime, timedelta

def compute_rolling_stats(closes):
    n = len(closes)
    if n < 80:
        return [], []
    
    # Daily returns
    returns = [(closes[i] / closes[i-1]) - 1 for i in range(1, n)]
    
    # Rolling 20-day std dev
    std20 = [None] * n
    for i in range(20, len(returns) + 1):
        window = returns[i-20:i]
        mean = sum(window) / 20
        var = sum((x - mean) ** 2 for x in window) / 19
        std20[i] = math.sqrt(var)
    
    # Rolling 60-day min of std20
    min60 = [None] * n
    for i in range(79, n):
        if std20[i] is None:
            continue
        window = [std20[j] for j in range(i-59, i+1)