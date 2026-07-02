import os
from bs4 import BeautifulSoup

def main():
    html_file = 'index.old.html'
    if not os.path.exists(html_file):
        print(f"File {html_file} not found!")
        return

    with open(html_file, 'r', encoding='utf-8') as f:
        content = f.read()

    soup = BeautifulSoup(content, 'html.parser')

    modals = [
        'detailsModal', 'pricingModal', 'retryErrorLogsModal', 
        'updateModal', 'sessionBindingsModal', 'exportPacketsModal',
        'remoteModal', 'relayUserModal', 'relayUserStatsModal', 'relayUserQuotaModal'
    ]

    os.makedirs('src/components/modals', exist_ok=True)
    
    app_vue_imports = []
    app_vue_components = []

    for modal_id in modals:
        div = soup.find('div', id=modal_id)
        if div:
            html_content = str(div)
            # Capitalize first letter
            comp_name = modal_id[0].upper() + modal_id[1:]
            vue_content = f"""<template>
{html_content}
</template>

<script setup lang="ts">
import {{ onMounted }} from 'vue';

onMounted(() => {{
}});
</script>
"""
            with open(f'src/components/modals/{comp_name}.vue', 'w', encoding='utf-8') as f:
                f.write(vue_content)
            print(f"Created {comp_name}.vue")
            app_vue_imports.append(f"import {comp_name} from './components/modals/{comp_name}.vue';")
            app_vue_components.append(f"<{comp_name} />")
        else:
            print(f"Modal {modal_id} not found!")

    print("\n--- Add these to App.vue script setup ---")
    print("\n".join(app_vue_imports))
    print("\n--- Add these to App.vue template (at the bottom) ---")
    print("\n".join(app_vue_components))

if __name__ == '__main__':
    main()
