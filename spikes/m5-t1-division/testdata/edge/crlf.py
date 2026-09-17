def crlf_stage(name):
    return name

def crlf_pipeline(stages):
    return [crlf_stage(s) for s in stages]
