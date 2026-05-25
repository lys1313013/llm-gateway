import json

def read_config():
    """读取并解析config.json文件"""
    with open('config.json', 'r') as f:
        return json.load(f)

def get_proxy_config():
    """获取代理配置"""
    config = read_config()
    return config.get('proxy_config', {})

def get_mode():
    """获取当前模式"""
    config = read_config()
    return config.get('mode', 'mock')
