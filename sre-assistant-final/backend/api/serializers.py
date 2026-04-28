from rest_framework import serializers
from .models import FaultRecord

class FaultRecordSerializer(serializers.ModelSerializer):
    class Meta:
        model = FaultRecord
        fields = '__all__'
