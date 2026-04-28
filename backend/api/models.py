from django.db import models

class FaultRecord(models.Model):
    fault_id = models.CharField(max_length=255, db_index=True)
    alert_name = models.CharField(max_length=128)
    namespace = models.CharField(max_length=128, blank=True, default='')
    name = models.CharField(max_length=255, blank=True, default='')
    action = models.CharField(max_length=128, blank=True, default='')
    status = models.CharField(max_length=64, default='running')
    message = models.TextField(blank=True, default='')
    raw_alert = models.JSONField(default=dict)
    started_at = models.DateTimeField()
    finished_at = models.DateTimeField(null=True, blank=True)

    class Meta:
        db_table = 'fault_records'
        ordering = ['-started_at']
