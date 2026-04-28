FROM python:3.11-slim
WORKDIR /app
ENV PYTHONDONTWRITEBYTECODE=1 PYTHONUNBUFFERED=1
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt
COPY app.py .
COPY sre_manual.txt /data/sre_manual.txt
EXPOSE 8000
CMD ["gunicorn", "-b", "0.0.0.0:8000", "app:app", "--workers", "1", "--timeout", "120"]
