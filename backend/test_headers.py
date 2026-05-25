from werkzeug.datastructures import Headers, EnvironHeaders
h = Headers()
h.add("X-Test", "1")
h.add("X-Test", "2")
print("dict:", dict(h))
# print("to_dict:", h.to_dict(flat=False))
